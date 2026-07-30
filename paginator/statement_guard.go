package paginator

import (
	"fmt"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type statementGuardMode uint8

const (
	guardData statementGuardMode = iota
	guardCount
)

// statementGuard 在 GORM 执行完全部 scope、构建 SQL 之前校验最终语句，
// 再把临时 Dest 切回真实扫描目标。它只依赖每次查询私有的 Statement，不注册全局 callback。
type statementGuard struct {
	dest        any
	projection  projection
	countSelect clauseState
	order       clause.OrderBy
	limit       int
	offset      int
	mode        statementGuardMode
	applied     bool
}

func (g *statementGuard) ModifyStatement(stmt *gorm.Statement) {
	g.applied = true
	stmt.Dest = g.dest
	if stmt.SQL.Len() > 0 {
		_ = stmt.AddError(rawQueryError())

		return
	}

	intact := false
	switch g.mode {
	case guardData:
		intact = paginationIntact(stmt, g.limit, g.offset) &&
			orderIntact(stmt, g.order, g.projection.restricted) &&
			g.projection.dataIntact(stmt)
	case guardCount:
		orderSafe := countOrderSafe(stmt)
		delete(stmt.Clauses, orderByClauseName)
		intact = paginationIntact(stmt, noLimit, 0) &&
			orderSafe &&
			g.projection.countShapeIntact(stmt) &&
			g.countSelect.equal(snapshotClause(stmt, selectClauseName))
	}
	if !intact {
		_ = stmt.AddError(ErrDeferredPaginationClause)
	}
}

// countOrderSafe 允许普通追加排序（它在数据查询里排在本包稳定排序之后，不改变行序），
// 但拒绝 Reorder 与 Expression：二者会在随后的数据查询里截断或顶替本包排序。
// 允许的追加排序由 Count guard 在校验后删除，避免聚合统计携带无意义甚至非法的 ORDER BY。
func countOrderSafe(stmt *gorm.Statement) bool {
	c, ok := stmt.Clauses[orderByClauseName]
	if !ok {
		return true
	}
	order, isOrderBy := c.Expression.(clause.OrderBy)
	if !isOrderBy || order.Expression != nil {
		return false
	}
	for _, column := range order.Columns {
		if column.Reorder {
			return false
		}
	}

	return true
}

func rawQueryError() error {
	return fmt.Errorf(
		"%w: raw SQL statements are unsupported because GORM does not rebuild pagination clauses",
		ErrDeferredPaginationClause,
	)
}

// paginationIntact 校验 Statement 中的 LIMIT/OFFSET 仍是本包设置的值。
//
// 执行前 guard 在全部 scope 结束后调用；执行后再调用一次作为 callback 改写的防御层。
// 两者都不发额外 SQL、不重跑调用方 scope。
func paginationIntact(stmt *gorm.Statement, wantLimit, wantOffset int) bool {
	c, ok := stmt.Clauses[limitClauseName]
	if !ok {
		return false
	}
	limit, isLimit := c.Expression.(clause.Limit)
	if !isLimit || limit.Limit == nil {
		return false
	}

	return *limit.Limit == wantLimit && limit.Offset == wantOffset
}

// orderIntact 校验本包设置的排序仍决定最终行序。
//
// 三个必要条件：
//   - Expression 必须为 nil。clause.OrderBy.Build 一旦发现 Expression 非 nil 就**完全忽略
//     Columns**，而它的 MergeClause 会把本包的 Columns 原样复制进对方、只保留对方的
//     Expression——于是 Columns 看着还是本包的，实际执行的却是对方的表达式
//     （实测嵌套 scope 注入 clause.OrderBy{Expression: Expr{SQL: "RAND()"}} 后
//     生成 ORDER BY RAND()，OFFSET 分页因此重复与遗漏）；
//   - 本包的列必须仍在最前（用 Reorder 截断本包排序的写法会被拦下）；
//   - exact 为真（受限投影）时不允许任何追加列：DISTINCT / GROUP BY 下追加投影外的列
//     会被数据库拒绝，且本包此时不追加主键次级排序、行序完全依赖调用方给的那一列。
//     完整投影下追加列排在本包主键次级排序之后，不影响行序，故放行。
func orderIntact(stmt *gorm.Statement, want clause.OrderBy, exact bool) bool {
	c, ok := stmt.Clauses[orderByClauseName]
	if !ok {
		return false
	}
	got, isOrderBy := c.Expression.(clause.OrderBy)
	if !isOrderBy || got.Expression != nil {
		return false
	}
	if exact {
		return slices.Equal(got.Columns, want.Columns)
	}
	if len(got.Columns) < len(want.Columns) {
		return false
	}

	return slices.Equal(got.Columns[:len(want.Columns)], want.Columns)
}

// applyPaginationScope 返回设置本包排序与分页的尾随 scope。
//
// 必须以 scope 形式下发：GORM 的 Scopes 在查询执行阶段才运行，若在构建前直接设置，
// 调用方 scope 里的 Order/Limit/Offset 会后到并覆盖或追加（GORM 官方文档正把分页
// 列为 Scopes 的典型用法）。追加在最后的 scope 反过来后发制人：
//   - ORDER BY 首列带 Reorder，合并时截断并丢弃先前所有排序列；
//   - Limit 非零值直接胜出；
//   - Offset 先置 -1 归零再设目标值——GORM 的合并规则会让「0」被先前的非零 offset
//     覆盖（实测第 1 页会拿到调用方 scope 的 offset），必须经 -1 显式归零。
func applyPaginationScope(
	order clause.OrderBy,
	pageSize, offset int,
	guard *statementGuard,
) func(*gorm.DB) *gorm.DB {
	return func(d *gorm.DB) *gorm.DB {
		d = d.Clauses(order).Limit(pageSize).Offset(noLimit).Offset(offset)
		d.Statement.Dest = guard

		return d
	}
}

// stripPaginationScope 是统计用的尾随 scope：在调用方 scope 之后清除排序与分页，
// 避免 Count 被 LIMIT/OFFSET 截断（截断后 count 查询返回 0 行 → 总数静默为 0）。
func stripPaginationScope(guard *statementGuard) func(*gorm.DB) *gorm.DB {
	return func(d *gorm.DB) *gorm.DB {
		delete(d.Statement.Clauses, orderByClauseName)
		d = d.Limit(noLimit).Offset(noLimit)
		d.Statement.Dest = guard

		return d
	}
}
