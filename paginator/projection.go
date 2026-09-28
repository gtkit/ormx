package paginator

import (
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/utils"
)

// projection 描述查询投影对分页的影响，并保存 scope 执行前的结构快照。
type projection struct {
	// restricted 投影或分组限定了可出现在 ORDER BY 的列：
	// 自动主键次级排序会被数据库拒绝，故要求调用方显式提供排序列。
	restricted bool
	// unreliableCount GORM 的 Count 在该写法下失去去重语义，必须由调用方提供总数。
	unreliableCount bool

	statementDistinct bool
	distinct          bool
	selects           []string
	selectClause      clauseState
	groupClause       clauseState
}

type clauseState struct {
	present    bool
	expression clause.Expression
}

// projectionOf 判定查询投影。任何 DISTINCT 都要求 WithTotal：单列
// COUNT(DISTINCT col) 排除 NULL，而 SELECT DISTINCT col 会保留一个 NULL；其余写法还可能
// 被 GORM 退化为 count(*)。GROUP BY 则继续按 GORM RowsAffected 统计分组数。
func projectionOf(stmt *gorm.Statement) projection {
	proj := projection{
		statementDistinct: stmt.Distinct,
		distinct:          hasDistinctProjection(stmt),
		selects:           slices.Clone(stmt.Selects),
		selectClause:      snapshotClause(stmt, selectClauseName),
		groupClause:       snapshotClause(stmt, groupByClauseName),
	}
	proj.restricted = proj.distinct || proj.groupClause.present
	proj.unreliableCount = proj.distinct

	return proj
}

func hasDistinctProjection(stmt *gorm.Statement) bool {
	if stmt.Distinct {
		return true
	}
	if c, ok := stmt.Clauses[selectClauseName]; ok {
		switch expr := c.Expression.(type) {
		case clause.Select:
			if expr.Distinct {
				return true
			}
		case clause.Expr:
			// clause.Select{Expression: clause.Expr{SQL: "DISTINCT …"}}：结构化外壳套原始串，
			// 合并后只剩内层 Expr（Select.MergeClause 的行为），故按原始串判定
			if hasDistinctPrefix(expr.SQL) {
				return true
			}
		case clause.NamedExpr:
			if hasDistinctPrefix(expr.SQL) {
				return true
			}
		}
	}

	return rawDistinctSelect(stmt.Selects)
}

func (p projection) dataIntact(stmt *gorm.Statement) bool {
	return p.shapeIntact(stmt) && p.selectClause.equal(snapshotClause(stmt, selectClauseName))
}

func (p projection) countShapeIntact(stmt *gorm.Statement) bool {
	return p.shapeIntact(stmt)
}

func (p projection) shapeIntact(stmt *gorm.Statement) bool {
	return p.statementDistinct == stmt.Distinct &&
		p.distinct == hasDistinctProjection(stmt) &&
		slices.Equal(p.selects, stmt.Selects) &&
		p.groupClause.equal(snapshotClause(stmt, groupByClauseName))
}

func snapshotClause(stmt *gorm.Statement, name string) clauseState {
	c, ok := stmt.Clauses[name]
	if !ok {
		return clauseState{}
	}

	return clauseState{present: true, expression: cloneProjectionExpression(c.Expression)}
}

func (s clauseState) equal(other clauseState) bool {
	return s.present == other.present && reflect.DeepEqual(s.expression, other.expression)
}

func cloneProjectionExpression(expr clause.Expression) clause.Expression {
	switch value := expr.(type) {
	case clause.Select:
		value.Columns = slices.Clone(value.Columns)
		value.Expression = cloneProjectionExpression(value.Expression)

		return value
	case clause.Expr:
		value.Vars = slices.Clone(value.Vars)

		return value
	case clause.NamedExpr:
		value.Vars = slices.Clone(value.Vars)

		return value
	case clause.GroupBy:
		value.Columns = slices.Clone(value.Columns)
		value.Having = slices.Clone(value.Having)
		for i := range value.Having {
			value.Having[i] = cloneProjectionExpression(value.Having[i])
		}

		return value
	default:
		return expr
	}
}

// countStarSQL 是 GORM Count 在无自定义 Select 时生成的默认统计表达式。
const countStarSQL = "count(*)"

// expectedCountSelect 复现 GORM v1.31.2 Count 在执行 scope 前生成的 SELECT Clause，
// 让执行前 guard 能区分 GORM 的正常改写与 scope 对统计投影的再次覆盖。
func expectedCountSelect(stmt *gorm.Statement) clauseState {
	if len(stmt.Selects) == 0 {
		return clauseState{present: true, expression: clause.Expr{SQL: countStarSQL}}
	}
	if strings.HasPrefix(strings.TrimSpace(strings.ToLower(stmt.Selects[0])), "count(") {
		return snapshotClause(stmt, selectClauseName)
	}

	return clauseState{present: true, expression: expectedCountExpression(stmt)}
}

func expectedCountExpression(stmt *gorm.Statement) clause.Expr {
	dbName, ok := countColumnName(stmt)
	if !ok {
		return clause.Expr{SQL: countStarSQL}
	}
	if stmt.Distinct {
		return clause.Expr{SQL: "COUNT(DISTINCT(?))", Vars: []any{clause.Column{Name: dbName}}}
	}
	if dbName != "*" {
		return clause.Expr{SQL: "COUNT(?)", Vars: []any{clause.Column{Name: dbName}}}
	}

	return clause.Expr{SQL: countStarSQL}
}

func countColumnName(stmt *gorm.Statement) (string, bool) {
	if len(stmt.Selects) != 1 {
		return "", false
	}
	dbName := stmt.Selects[0]
	fields := strings.FieldsFunc(dbName, utils.IsInvalidDBNameChar)
	if len(fields) != 1 && (len(fields) != 3 || (!strings.EqualFold(fields[1], "AS") && fields[1] != ".")) {
		return "", false
	}
	if stmt.Schema != nil {
		if field := stmt.Schema.LookUpField(dbName); field != nil {
			dbName = field.DBName
		}
	}

	return dbName, true
}

// rawDistinctSelect 判断 Select 是否以原始字符串形式写了 DISTINCT
// （如 Select("DISTINCT name")）——这种写法不会置 Statement.Distinct。
func rawDistinctSelect(selects []string) bool {
	return len(selects) > 0 && hasDistinctPrefix(selects[0])
}

// hasDistinctPrefix 判断原始 SQL 片段是否以去重关键字开头
// （DISTINCT 及 MySQL 的同义词 DISTINCTROW；要求其后是分隔符，
// 避免把 distinct_id 之类的列名误判）。
func hasDistinctPrefix(sql string) bool {
	for {
		sql = strings.TrimLeftFunc(sql, unicode.IsSpace)
		switch {
		case strings.HasPrefix(sql, "/*!"):
			end := strings.Index(sql[3:], "*/")
			if end < 0 {
				return false
			}
			body := strings.TrimLeftFunc(sql[3:3+end], func(r rune) bool {
				return unicode.IsSpace(r) || unicode.IsDigit(r)
			})
			if hasDistinctKeyword(body) {
				return true
			}
			sql = sql[3+end+2:]
		case strings.HasPrefix(sql, "/*"):
			end := strings.Index(sql[2:], "*/")
			if end < 0 {
				return false
			}
			sql = sql[end+4:]
		case strings.HasPrefix(sql, "--"), strings.HasPrefix(sql, "#"):
			end := strings.IndexAny(sql, "\r\n")
			if end < 0 {
				return false
			}
			sql = sql[end+1:]
		default:
			return hasDistinctKeyword(sql)
		}
	}
}

func hasDistinctKeyword(sql string) bool {
	lower := strings.ToLower(sql)
	for _, kw := range [...]string{"distinctrow", "distinct"} {
		if !strings.HasPrefix(lower, kw) {
			continue
		}
		rest := lower[len(kw):]
		if rest == "" || rest[0] == '(' {
			return true
		}
		// 分隔符按 Unicode 空白判定：换行/回车/全角空格等同样是关键字边界
		// （只判 ' ' 与 '\t' 会让 "DISTINCT\nname" 漏检）
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsSpace(r) {
			return true
		}
	}

	return false
}
