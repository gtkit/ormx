package paginator

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// 测试模型：常规主键 / 自定义主键 / 无主键（覆盖 schema 边界）。
type widget struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"size:64"`
}

type coded struct {
	Code string `gorm:"primaryKey;size:32"`
	Name string `gorm:"size:64"`
}

type nopk struct {
	Name string `gorm:"size:64"`
}

// reservedWord 含 MySQL 保留字列名 order，用于验证方言引擎加引号。
type reservedWord struct {
	ID    int64 `gorm:"primaryKey"`
	Order int   `gorm:"column:order"`
}

// ---------------------------------------------------------------------------
// 脚本化 fake driver：按 SQL 内容分流 count / find，记录 SQL 与参数供断言。
// 仿照根包 testhelper 的自建 stub 先例，不引入 sqlmock 等外部依赖。
// ---------------------------------------------------------------------------

type scriptState struct {
	mu      sync.Mutex
	total   int64
	rows    []widget
	countEr error
	findEr  error
	// norecord 关闭 SQL 记录：benchmark 只测本包开销，不含锁与切片增长
	norecord bool
	// singleCol 只返回 id 一列：用于扫描进基础类型切片（如 []int）的用例
	singleCol bool

	queries []string
	args    [][]driver.Value
}

func (s *scriptState) record(query string, args []driver.NamedValue) {
	if s.norecord {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	vals := make([]driver.Value, 0, len(args))
	for _, a := range args {
		vals = append(vals, a.Value)
	}
	s.queries = append(s.queries, query)
	s.args = append(s.args, vals)
}

func (s *scriptState) recorded() ([]string, [][]driver.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.queries...), append([][]driver.Value(nil), s.args...)
}

type scriptConnector struct{ state *scriptState }

func (c *scriptConnector) Connect(context.Context) (driver.Conn, error) {
	return &scriptConn{state: c.state}, nil
}
func (c *scriptConnector) Driver() driver.Driver { return scriptDriver{state: c.state} }

type scriptDriver struct{ state *scriptState }

func (d scriptDriver) Open(string) (driver.Conn, error) { return &scriptConn{state: d.state}, nil }

type scriptConn struct{ state *scriptState }

func (c *scriptConn) Prepare(query string) (driver.Stmt, error) {
	return &scriptStmt{state: c.state, query: query}, nil
}
func (c *scriptConn) Close() error              { return nil }
func (c *scriptConn) Begin() (driver.Tx, error) { return nil, driver.ErrSkip }

type scriptStmt struct {
	state *scriptState
	query string
}

func (s *scriptStmt) Close() error  { return nil }
func (s *scriptStmt) NumInput() int { return -1 }
func (s *scriptStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (s *scriptStmt) Query(args []driver.Value) (driver.Rows, error) {
	named := make([]driver.NamedValue, 0, len(args))
	for i, v := range args {
		named = append(named, driver.NamedValue{Ordinal: i + 1, Value: v})
	}
	s.state.record(s.query, named)

	// 判别用前缀而非 contains："SELECT count(" 只可能是 GORM Count 生成的统计查询；
	// 用 contains "count(" 会把选择列表含聚合表达式的数据查询误判为 count（曾掩盖 GROUP BY 缺陷）。
	if isCountQuery(s.query) {
		if s.state.countEr != nil {
			return nil, s.state.countEr
		}
		// 分组统计在真实数据库中每个分组返回一行；GORM 的 Count 对带 GROUP BY 的语句
		// 一律用 *count = tx.RowsAffected（gorm@v1.31.2 finisher_api.go）覆盖扫描值，
		// 所以替身必须返回 total 行，否则 total 恒为 1，与真实语义不符。
		if isGroupedQuery(s.query) {
			values := make([][]driver.Value, 0, s.state.total)
			for range s.state.total {
				values = append(values, []driver.Value{int64(1)})
			}
			return &scriptRows{columns: []string{"count(*)"}, values: values}, nil
		}
		return &scriptRows{columns: []string{"count(*)"}, values: [][]driver.Value{{s.state.total}}}, nil
	}
	if s.state.findEr != nil {
		return nil, s.state.findEr
	}
	values := make([][]driver.Value, 0, len(s.state.rows))
	if s.state.singleCol {
		for _, r := range s.state.rows {
			values = append(values, []driver.Value{r.ID})
		}
		return &scriptRows{columns: []string{"id"}, values: values}, nil
	}
	for _, r := range s.state.rows {
		values = append(values, []driver.Value{r.ID, r.Name})
	}
	return &scriptRows{columns: []string{"id", "name"}, values: values}, nil
}

type scriptRows struct {
	columns []string
	values  [][]driver.Value
	pos     int
}

func (r *scriptRows) Columns() []string { return r.columns }
func (r *scriptRows) Close() error      { return nil }
func (r *scriptRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.pos])
	r.pos++
	return nil
}

func newScriptedDB(t *testing.T, state *scriptState) *gorm.DB {
	t.Helper()
	sqlDB := sql.OpenDB(&scriptConnector{state: state})
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	return db
}

// isCountQuery 判定是否为 GORM Count 生成的统计查询（按前缀，见 scriptStmt.Query 注释）。
func isCountQuery(query string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "select count(")
}

// isGroupedQuery 判定语句是否带 GROUP BY，决定 count 替身返回单行还是分组行。
func isGroupedQuery(query string) bool {
	return strings.Contains(strings.ToUpper(query), "GROUP BY")
}

func countSQL(queries []string) (string, bool) {
	for _, q := range queries {
		if isCountQuery(q) {
			return q, true
		}
	}
	return "", false
}

func dataSQL(queries []string) (string, bool) {
	for _, q := range queries {
		if !isCountQuery(q) {
			return q, true
		}
	}
	return "", false
}

// orderByPart 截取 SQL 中 ORDER BY 之后的片段，便于精确断言排序列组成。
func orderByPart(query string) string {
	i := strings.Index(query, "ORDER BY")
	if i < 0 {
		return ""
	}
	part := query[i:]
	if j := strings.Index(part, " LIMIT"); j >= 0 {
		part = part[:j]
	}
	return part
}

// ---------------------------------------------------------------------------
// A. 输入信任边界（Params 全部远端可控）
// ---------------------------------------------------------------------------

func TestPageSizeCapPreventsUnboundedQueryAndOverflow(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 250, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	got, err := Paginate[widget](db.Model(&widget{}), Params{Page: 1, PageSize: math.MaxInt})
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if got.PageSize != defaultMaxPageSize {
		t.Fatalf("PageSize = %d, want 钳制到默认上限 %d", got.PageSize, defaultMaxPageSize)
	}
	if got.TotalPage != 3 || got.CurrentPage != 1 {
		t.Fatalf("TotalPage=%d CurrentPage=%d, want 3/1（无负值、无溢出）", got.TotalPage, got.CurrentPage)
	}
	_, args := state.recorded()
	for _, a := range args[len(args)-1] {
		if v, ok := a.(int64); ok && v > int64(defaultMaxPageSize) {
			t.Fatalf("SQL 参数出现未钳制的 LIMIT: %v", args)
		}
	}
}

func TestPageSizeAndPageNormalization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   Params
		opts     []Option
		wantSize int
		wantPage int
	}{
		{name: "零值取默认", params: Params{}, wantSize: defaultPageSize, wantPage: 1},
		{name: "负值取默认", params: Params{Page: -3, PageSize: -5}, wantSize: defaultPageSize, wantPage: 1},
		{name: "页码越界钳到末页", params: Params{Page: 999, PageSize: 10}, wantSize: 10, wantPage: 3},
		{name: "自定义上限", params: Params{PageSize: 400}, opts: []Option{WithMaxPageSize(500)}, wantSize: 400, wantPage: 1},
		{name: "超自定义上限钳制", params: Params{PageSize: 900}, opts: []Option{WithMaxPageSize(500)}, wantSize: 500, wantPage: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := &scriptState{total: 25, rows: []widget{{1, "a"}}}
			db := newScriptedDB(t, state)

			got, err := Paginate[widget](db.Model(&widget{}), tt.params, tt.opts...)
			if err != nil {
				t.Fatalf("Paginate() error = %v", err)
			}
			if got.PageSize != tt.wantSize || got.CurrentPage != tt.wantPage {
				t.Fatalf("size=%d page=%d, want %d/%d", got.PageSize, got.CurrentPage, tt.wantSize, tt.wantPage)
			}
		})
	}
}

func TestSortInjectionFallsBackToDefault(t *testing.T) {
	t.Parallel()

	for _, sort := range []string{"name;DROP TABLE users", "id --", "created_at desc", "列名", "id`"} {
		state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
		db := newScriptedDB(t, state)

		if _, err := Paginate[widget](db.Model(&widget{}), Params{Sort: sort, Order: "desc"}); err != nil {
			t.Fatalf("Paginate(sort=%q) error = %v", sort, err)
		}
		q, _ := dataSQL(func() []string { qs, _ := state.recorded(); return qs }())
		if orderByPart(q) != "ORDER BY `widgets`.`id` DESC" {
			t.Fatalf("sort=%q 应回退主键排序, got %q", sort, q)
		}
		for _, meta := range []string{";", "--", "DROP", "列名"} {
			if strings.Contains(q, meta) {
				t.Fatalf("sort=%q 的注入片段 %q 进入了 SQL: %q", sort, meta, q)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// B. GORM 句柄语义（残留子句 / 零污染 / 并发复用）
// ---------------------------------------------------------------------------

func TestPaginatorOwnsPaginationClauses(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 30, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	// 入参句柄带残留 ORDER/LIMIT/OFFSET：全部由本包接管
	query := db.Model(&widget{}).Where("name <> ?", "x").Order("name desc").Limit(7).Offset(50)
	got, err := Paginate[widget](query, Params{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if got.CurrentPage != 1 {
		t.Fatalf("CurrentPage = %d, want 1", got.CurrentPage)
	}

	queries, args := state.recorded()
	cq, ok := countSQL(queries)
	if !ok {
		t.Fatal("缺少 count SQL")
	}
	for _, kw := range []string{"ORDER BY", "LIMIT", "OFFSET"} {
		if strings.Contains(cq, kw) {
			t.Fatalf("count SQL 不得继承 %s: %q", kw, cq)
		}
	}
	if !strings.Contains(cq, "name <>") {
		t.Fatalf("count SQL 应保留 WHERE 条件: %q", cq)
	}

	dq, hasData := dataSQL(queries)
	if !hasData {
		t.Fatal("缺少数据 SQL")
	}
	if strings.Contains(dq, "name desc") {
		t.Fatalf("数据 SQL 不得继承调用方 ORDER BY: %q", dq)
	}
	if orderByPart(dq) != "ORDER BY `widgets`.`id`" {
		t.Fatalf("数据 SQL 应为本包结构化排序（表限定主键）: %q", dq)
	}
	// 第 1 页 offset=0：LIMIT 参数必须是本包的 10 而非残留的 7/50
	last := args[len(args)-1]
	for _, a := range last {
		if v, isInt := a.(int64); isInt && (v == 7 || v == 50) {
			t.Fatalf("数据 SQL 残留调用方 LIMIT/OFFSET 参数: %v", last)
		}
	}
}

// TestScopesInjectedPaginationOverridden Scopes 在执行阶段才物化，会后到覆盖本包的
// 三子句（GORM 官方文档正把分页列为 Scopes 的典型用法）。本包以尾随 scope 下发，
// 必须反过来后发制人：统计不被截断、排序不被追加、第 1 页 offset 归零。
func TestScopesInjectedPaginationOverridden(t *testing.T) {
	t.Parallel()

	scopePaginate := func(page, size int) func(*gorm.DB) *gorm.DB {
		return func(d *gorm.DB) *gorm.DB {
			return d.Where("name <> ?", "x").Order("name DESC").Offset((page - 1) * size).Limit(size)
		}
	}

	// 第 1 页：scope 的 OFFSET 5 必须被归零（GORM 的合并规则里 0 会输给先前的非零值）
	state := &scriptState{total: 30, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	got, err := Paginate[widget](db.Model(&widget{}).Scopes(scopePaginate(2, 5)),
		Params{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if got.TotalCount != 30 || got.TotalPage != 3 {
		t.Fatalf("total=%d pages=%d, want 30/3（统计被 scope 的 LIMIT 截断）", got.TotalCount, got.TotalPage)
	}

	recorded, args := state.recorded()
	cq, ok := countSQL(recorded)
	if !ok {
		t.Fatal("缺少 count SQL")
	}
	for _, kw := range []string{"ORDER BY", "LIMIT", "OFFSET"} {
		if strings.Contains(cq, kw) {
			t.Fatalf("count SQL 不得继承 scope 的 %s: %q", kw, cq)
		}
	}
	if !strings.Contains(cq, "name <>") {
		t.Fatalf("count SQL 应保留 scope 的 WHERE 条件: %q", cq)
	}

	dq, hasData := dataSQL(recorded)
	if !hasData {
		t.Fatal("缺少数据 SQL")
	}
	if orderByPart(dq) != "ORDER BY `widgets`.`id`" {
		t.Fatalf("数据 SQL 排序应只由本包决定: %q", dq)
	}
	// offset=0 时 GORM 不写 OFFSET 子句；出现 OFFSET 即说明 scope 的 5 未被归零
	if strings.Contains(dq, "OFFSET") {
		t.Fatalf("第 1 页不得残留 scope 的 OFFSET: %q", dq)
	}
	if last := args[len(args)-1]; !slices.Contains(last, driver.Value(int64(10))) {
		t.Fatalf("数据 SQL LIMIT 参数 = %v, want 含 10", last)
	}

	// 第 2 页：offset 必须是本包算出的 10，而非 scope 的 5
	state2 := &scriptState{total: 30, rows: []widget{{1, "a"}}}
	db2 := newScriptedDB(t, state2)
	if _, err2 := Paginate[widget](db2.Model(&widget{}).Scopes(scopePaginate(2, 5)),
		Params{Page: 2, PageSize: 10}); err2 != nil {
		t.Fatalf("第 2 页 Paginate() error = %v", err2)
	}
	recorded2, args2 := state2.recorded()
	dq2, _ := dataSQL(recorded2)
	if !strings.Contains(dq2, "OFFSET") {
		t.Fatalf("第 2 页应有 OFFSET: %q", dq2)
	}
	last2 := args2[len(args2)-1]
	if !slices.Contains(last2, driver.Value(int64(10))) || slices.Contains(last2, driver.Value(int64(5))) {
		t.Fatalf("第 2 页 LIMIT/OFFSET 参数 = %v, want 含 10 且不含 scope 的 5", last2)
	}
}

// TestNestedScopeOverrideRejected 嵌套 scope（scope 内部再注册 scope）会被 GORM 排到
// 下一轮、也就是本包尾随 scope 之后，从而覆盖 LIMIT/OFFSET——真实数据库上 count 查询
// 因此返回 0 行、总数静默为 0、翻页返回空页。必须在执行后按事实校验并报错。
func TestNestedScopeOverrideRejected(t *testing.T) {
	t.Parallel()

	nest := func(inner func(*gorm.DB) *gorm.DB) func(*gorm.DB) *gorm.DB {
		return func(d *gorm.DB) *gorm.DB {
			return d.Scopes(inner)
		}
	}

	tests := []struct {
		name    string
		inner   func(*gorm.DB) *gorm.DB
		wantErr error
	}{
		{
			name:    "覆盖 LIMIT/OFFSET",
			inner:   func(d *gorm.DB) *gorm.DB { return d.Limit(2).Offset(20) },
			wantErr: ErrDeferredPaginationClause,
		},
		{
			name:    "只覆盖 OFFSET",
			inner:   func(d *gorm.DB) *gorm.DB { return d.Offset(20) },
			wantErr: ErrDeferredPaginationClause,
		},
		{
			name: "用 Reorder 截断本包排序",
			inner: func(d *gorm.DB) *gorm.DB {
				return d.Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{
					{Column: clause.Column{Name: "name"}, Reorder: true},
				}})
			},
			wantErr: ErrDeferredPaginationClause,
		},
		{
			name:  "追加次级排序列无害（本包列仍在最前）",
			inner: func(d *gorm.DB) *gorm.DB { return d.Order("name DESC") },
		},
		{
			name:  "纯条件嵌套 scope 无害",
			inner: func(d *gorm.DB) *gorm.DB { return d.Where("name <> ?", "x") },
		},
		{
			name:  "零值 LIMIT/OFFSET 不误判（GORM 合并时会保留本包的值）",
			inner: func(d *gorm.DB) *gorm.DB { return d.Limit(0).Offset(0) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := &scriptState{total: 25, rows: []widget{{1, "a"}}}
			db := newScriptedDB(t, state)
			got, err := Paginate[widget](db.Model(&widget{}).Scopes(nest(tt.inner)),
				Params{Page: 1, PageSize: 5})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Paginate() error = %v", err)
			}
			if got.TotalCount != 25 || len(got.Items) != 1 {
				t.Fatalf("total=%d items=%d, want 25/1", got.TotalCount, len(got.Items))
			}
		})
	}
}

func TestCallerHandleNotPolluted(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	caller := db.Model(&widget{}).Where("name <> ?", "x")

	if _, err := Paginate[widget](caller, Params{Page: 2, PageSize: 10}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}

	// 复用调用方句柄：不得带上本包的 ORDER/LIMIT/OFFSET
	var out []widget
	if err := caller.Find(&out).Error; err != nil {
		t.Fatalf("caller reuse Find() error = %v", err)
	}
	queries, _ := state.recorded()
	reuse := queries[len(queries)-1]
	for _, kw := range []string{"ORDER BY", "LIMIT", "OFFSET"} {
		if strings.Contains(reuse, kw) {
			t.Fatalf("调用方句柄被污染（复用出现 %s）: %q", kw, reuse)
		}
	}
}

// TestCallerStatementUntouched 确定性断言入参句柄的 Statement 未被写入。
//
// schemaOf 里的 Parse 会写 Statement.Schema/Table，而 db.Session(&gorm.Session{})
// 只把 clone 置为 2、Statement 指针仍与入参共享——必须由 privateSession 立即克隆。
// -race 只在并发交错时才拦得住且报错指向 GORM 内部，这里把契约变成确定性回归。
func TestCallerStatementUntouched(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	caller := db.Model(&widget{}).Where("name <> ?", "x")

	// 前提：Paginate 之前调用方句柄尚未 Parse 过
	if caller.Statement.Schema != nil || caller.Statement.Table != "" {
		t.Fatalf("前提不成立: schema=%v table=%q", caller.Statement.Schema, caller.Statement.Table)
	}
	wantClauses := len(caller.Statement.Clauses)

	if _, err := Paginate[widget](caller, Params{PageSize: 5}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}

	if caller.Statement.Schema != nil {
		t.Errorf("入参句柄 Statement.Schema 被写入（Statement 未私有化）")
	}
	if caller.Statement.Table != "" {
		t.Errorf("入参句柄 Statement.Table = %q, want 空", caller.Statement.Table)
	}
	if got := len(caller.Statement.Clauses); got != wantClauses {
		t.Errorf("入参句柄子句数 = %d, want %d", got, wantClauses)
	}
}

func TestConcurrentPaginateOnSharedHandle(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 40, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	caller := db.Model(&widget{}).Where("name <> ?", "x")

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func(page int) {
			defer wg.Done()
			if _, err := Paginate[widget](caller, Params{Page: page, PageSize: 10}); err != nil {
				errs <- err
			}
		}(i + 1)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 Paginate 失败: %v", err)
	}
}

// ---------------------------------------------------------------------------
// C. Schema 边界（自定义主键 / tiebreaker / 无主键）
// ---------------------------------------------------------------------------

func TestDefaultSortUsesModelPrimaryKey(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[coded](db.Model(&coded{}), Params{}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `codeds`.`code`" {
		t.Fatalf("自定义主键应作默认排序（表限定）且不重复追加 tiebreaker: %q", dq)
	}
}

func TestTiebreakerAppendedForStablePagination(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[widget](db.Model(&widget{}), Params{Sort: "name", Order: "desc"}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `widgets`.`name` DESC,`widgets`.`id` DESC" {
		t.Fatalf("非唯一排序列应追加表限定主键 tiebreaker（方向跟随主排序）: %q", dq)
	}
}

func TestNoPrimaryKeyModel(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	// 无主键且无显式排序：宁可失败也不猜列名
	if _, err := Paginate[nopk](db.Model(&nopk{}), Params{}); !errors.Is(err, ErrNoSortColumn) {
		t.Fatalf("err = %v, want ErrNoSortColumn", err)
	}

	// 显式默认排序后可用，且无 tiebreaker（没有主键可追加）
	if _, err := Paginate[nopk](db.Model(&nopk{}), Params{}, WithDefaultSort("name")); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `nopks`.`name`" {
		t.Fatalf("无主键模型应仅按显式列排序（无 tiebreaker）: %q", dq)
	}
}

// ---------------------------------------------------------------------------
// D. Option 边界
// ---------------------------------------------------------------------------

func TestNilOptionSkipped(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 1, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[widget](db.Model(&widget{}), Params{}, nil, WithMaxPageSize(50), nil); err != nil {
		t.Fatalf("nil Option 不得导致失败: %v", err)
	}
}

func TestOptionValidation(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 1}
	db := newScriptedDB(t, state)

	tests := []struct {
		name string
		opt  Option
	}{
		{name: "上限非正", opt: WithMaxPageSize(0)},
		{name: "默认排序列非法", opt: WithDefaultSort("bad col")},
		{name: "映射值非法", opt: WithSortMapping(map[string]string{"k": "col;drop"})},
		{name: "总数为负", opt: WithTotal(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Paginate[widget](db.Model(&widget{}), Params{}, tt.opt); err == nil {
				t.Fatal("非法 Option 值应报错")
			}
		})
	}
}

func TestSortMappingAllowlist(t *testing.T) {
	t.Parallel()

	mapping := WithSortMapping(map[string]string{"created": "created_at"})

	t.Run("命中映射", func(t *testing.T) {
		t.Parallel()
		state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
		db := newScriptedDB(t, state)
		if _, err := Paginate[widget](db.Model(&widget{}), Params{Sort: "created"}, mapping); err != nil {
			t.Fatalf("Paginate() error = %v", err)
		}
		queries, _ := state.recorded()
		dq, _ := dataSQL(queries)
		// created_at 不是 widget 的字段（别名/外部列），故不加表限定；主键 tiebreaker 仍限定
		if orderByPart(dq) != "ORDER BY `created_at`,`widgets`.`id`" {
			t.Fatalf("应按映射列排序: %q", dq)
		}
	})

	t.Run("未命中回退默认_合法列名也不放行", func(t *testing.T) {
		t.Parallel()
		state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
		db := newScriptedDB(t, state)
		if _, err := Paginate[widget](db.Model(&widget{}), Params{Sort: "name"}, mapping); err != nil {
			t.Fatalf("Paginate() error = %v", err)
		}
		queries, _ := state.recorded()
		dq, _ := dataSQL(queries)
		if orderByPart(dq) != "ORDER BY `widgets`.`id`" {
			t.Fatalf("设置映射后任意列名不得直通: %q", dq)
		}
	})
}

func TestWithTotalSkipsCount(t *testing.T) {
	t.Parallel()

	t.Run("跳过count", func(t *testing.T) {
		t.Parallel()
		state := &scriptState{rows: []widget{{1, "a"}}}
		db := newScriptedDB(t, state)
		got, err := Paginate[widget](db.Model(&widget{}), Params{PageSize: 10}, WithTotal(42))
		if err != nil {
			t.Fatalf("Paginate() error = %v", err)
		}
		if got.TotalCount != 42 || got.TotalPage != 5 {
			t.Fatalf("total=%d pages=%d, want 42/5", got.TotalCount, got.TotalPage)
		}
		queries, _ := state.recorded()
		if _, hasCount := countSQL(queries); hasCount || len(queries) != 1 {
			t.Fatalf("WithTotal 应跳过 count，仅 1 条数据 SQL: %v", queries)
		}
	})

	t.Run("总数为零仅返回空页_零SQL", func(t *testing.T) {
		t.Parallel()
		state := &scriptState{}
		db := newScriptedDB(t, state)
		got, err := Paginate[widget](db.Model(&widget{}), Params{}, WithTotal(0))
		if err != nil {
			t.Fatalf("Paginate() error = %v", err)
		}
		if got.Items == nil || len(got.Items) != 0 || got.CurrentPage != 0 {
			t.Fatalf("空页契约: Items 非 nil 空切片、CurrentPage=0, got %+v", got)
		}
		queries, _ := state.recorded()
		if len(queries) != 0 {
			t.Fatalf("总数为零不应发出任何 SQL: %v", queries)
		}
	})
}

// ---------------------------------------------------------------------------
// E. 结果契约与错误路径
// ---------------------------------------------------------------------------

func TestItemsNeverNil(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 0}
	db := newScriptedDB(t, state)
	got, err := Paginate[widget](db.Model(&widget{}), Params{})
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if got.Items == nil {
		t.Fatal("Items 必须恒非 nil（JSON 序列化为 [] 而非 null）")
	}
}

func TestErrorPaths(t *testing.T) {
	t.Parallel()

	if _, err := Paginate[widget](nil, Params{}); !errors.Is(err, ErrNilDB) {
		t.Fatalf("nil db: err = %v, want ErrNilDB", err)
	}

	countErr := errors.New("count boom")
	state := &scriptState{countEr: countErr}
	if _, err := Paginate[widget](newScriptedDB(t, state).Model(&widget{}), Params{}); !errors.Is(err, countErr) ||
		!strings.Contains(err.Error(), "count total") {
		t.Fatalf("count 错误应包装并可穿透: %v", err)
	}

	findErr := errors.New("find boom")
	state2 := &scriptState{total: 5, findEr: findErr}
	if _, err := Paginate[widget](newScriptedDB(t, state2).Model(&widget{}), Params{}); !errors.Is(err, findErr) ||
		!strings.Contains(err.Error(), "query page") {
		t.Fatalf("find 错误应包装并可穿透: %v", err)
	}
}

// ---------------------------------------------------------------------------
// F. 纯函数
// ---------------------------------------------------------------------------

// wantMaxPages 以独立算式复算总页数期望值（向上取整 + offset/int 上界钳制），
// 与被测实现同逻辑但独立书写，用于极值断言且在 32 位平台同样成立。
func wantMaxPages(total int64, pageSize int) int {
	size := int64(pageSize)
	pages := total / size
	if total%size != 0 {
		pages++
	}
	if maxOffsetPages := int64(math.MaxInt) / size; pages-1 > maxOffsetPages {
		pages = maxOffsetPages + 1
	}
	if pages > int64(math.MaxInt) {
		pages = int64(math.MaxInt)
	}

	return int(pages)
}

func TestPureFunctions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		col  string
		want bool
	}{
		{"created_at", true},
		{"users.id", true},
		{"Field_9", true},
		{"_x", true},
		{"", false},
		{"id desc", false},
		{"id;", false},
		{"列", false},
		{"1", false},     // 位置排序 ORDER BY 1
		{"9a", false},    // 数字开头
		{".id", false},   // 空段
		{"id.", false},   // 空段
		{"a..b", false},  // 空段
		{"a.b.c", false}, // 超两段
	} {
		if got := isSafeColumn(tt.col); got != tt.want {
			t.Errorf("isSafeColumn(%q) = %v, want %v", tt.col, got, tt.want)
		}
	}

	for _, tt := range []struct {
		in, want string
	}{
		{"desc", "desc"}, {"DESC", "desc"}, {"", "asc"}, {"asc", "asc"}, {"desc; drop", "asc"},
	} {
		if got := normalizeOrder(tt.in); got != tt.want {
			t.Errorf("normalizeOrder(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	for _, tt := range []struct {
		total    int64
		size     int
		wantPage int
	}{
		{30, 10, 3},
		{31, 10, 4},
		{0, 10, 0},
		{1, 10, 1},
		{-5, 10, 0},
		{10, 0, 0},
		// 极值用例的期望值随平台 int 宽度变化：64 位下按向上取整算出，
		// 32 位下受 offset/int 上界保护钳制；用 wantMaxPages helper 统一表达，
		// 避免把 64 位常量写死在 int 字段里（32 位编译期溢出）。
		{math.MaxInt64, 100, wantMaxPages(math.MaxInt64, 100)},
		{math.MaxInt64, 1, wantMaxPages(math.MaxInt64, 1)},
		{math.MaxInt64 - 1, math.MaxInt, 1}, // 巨大 pageSize
	} {
		if got := calcTotalPage(tt.total, tt.size); got != tt.wantPage {
			t.Errorf("calcTotalPage(%d,%d) = %d, want %d", tt.total, tt.size, got, tt.wantPage)
		}
	}

	for _, tt := range []struct {
		size, maxSize, want int
	}{
		{0, 100, 10}, {-1, 100, 10}, {50, 100, 50}, {500, 100, 100}, {math.MaxInt, 100, 100},
	} {
		if got := clampPageSize(tt.size, tt.maxSize); got != tt.want {
			t.Errorf("clampPageSize(%d,%d) = %d, want %d", tt.size, tt.maxSize, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// G. Benchmark（fake driver：量本包开销，不含真实 DB I/O）
// ---------------------------------------------------------------------------

func BenchmarkPaginate(b *testing.B) {
	state := &scriptState{total: 1000, rows: []widget{{1, "a"}, {2, "b"}}, norecord: true}
	sqlDB := sql.OpenDB(&scriptConnector{state: state})
	defer sqlDB.Close()
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}),
		&gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		b.Fatal(err)
	}
	query := db.Model(&widget{}).Where("name <> ?", "x")

	b.ReportAllocs()
	for b.Loop() {
		if _, pErr := Paginate[widget](query, Params{Page: 3, PageSize: 20, Sort: "name"}); pErr != nil {
			b.Fatal(pErr)
		}
	}
}

// 复合主键模型（两个显式关闭自增的主键）。
type orderItem struct {
	OrderID int64 `gorm:"primaryKey;autoIncrement:false"`
	ItemID  int64 `gorm:"primaryKey;autoIncrement:false"`
	Qty     int
}

// TestExtremeOptionValuesDoNotPanic Option 值与 Params 同等视为不可信：
// WithMaxPageSize(MaxInt) + WithTotal(MaxInt64) + Page/PageSize 极值——
// 不 panic、元信息非负、offset 不溢出。
func TestExtremeOptionValuesDoNotPanic(t *testing.T) {
	t.Parallel()

	state := &scriptState{rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	got, err := Paginate[widget](db.Model(&widget{}),
		Params{Page: math.MaxInt, PageSize: math.MaxInt},
		WithMaxPageSize(math.MaxInt),
		WithTotal(math.MaxInt64),
	)
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if got.CurrentPage <= 0 || got.TotalPage <= 0 || got.PageSize <= 0 {
		t.Fatalf("极值输入下元信息必须为正: %+v", got)
	}
	_, args := state.recorded()
	for _, a := range args[len(args)-1] {
		if v, isInt := a.(int64); isInt && v < 0 {
			t.Fatalf("SQL 参数出现溢出负值: %v", args)
		}
	}
}

// TestCompositePrimaryKeySupported 复合主键：默认排序取第一主键，
// tiebreaker 追加其余全部主键列（均带表名限定）。
func TestCompositePrimaryKeySupported(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[orderItem](db.Model(&orderItem{}), Params{}); err != nil {
		t.Fatalf("复合主键模型不得返回错误: %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `order_items`.`order_id`,`order_items`.`item_id`" {
		t.Fatalf("复合主键应全集参与稳定排序: %q", dq)
	}
}

// TestJoinQueryUsesQualifiedTiebreaker Join 场景：主键排序必须带表名限定，防列歧义。
func TestJoinQueryUsesQualifiedTiebreaker(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	query := db.Model(&widget{}).Joins("JOIN others ON others.wid = widgets.id")
	if _, err := Paginate[widget](query, Params{Sort: "name"}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `widgets`.`name`,`widgets`.`id`" {
		t.Fatalf("Join 下排序列与 tiebreaker 必须表限定: %q", dq)
	}
}

// TestQualifiedSortDedupAgainstTiebreaker 限定名排序列与主键同列时不得重复生成相反排序。
func TestQualifiedSortDedupAgainstTiebreaker(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[widget](db.Model(&widget{}), Params{Sort: "widgets.id", Order: "desc"}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `widgets`.`id` DESC" {
		t.Fatalf("同列不同写法不得重复追加排序: %q", dq)
	}
}

// TestPresetDBErrorSurfaces 入参句柄携带的既有错误不得被任何路径吞掉（含 WithTotal 短路）。
func TestPresetDBErrorSurfaces(t *testing.T) {
	t.Parallel()

	boom := errors.New("preset boom")
	state := &scriptState{}
	db := newScriptedDB(t, state)
	caller := db.Model(&widget{})
	_ = caller.AddError(boom)

	if _, err := Paginate[widget](caller, Params{}, WithTotal(0)); !errors.Is(err, boom) {
		t.Fatalf("WithTotal(0) 短路不得吞掉入参错误: %v", err)
	}
	queries, _ := state.recorded()
	if len(queries) != 0 {
		t.Fatalf("携带错误的句柄不应发出 SQL: %v", queries)
	}
}

// TestSortMappingNilRejected nil 映射必须报错，不得静默降级关闭 allowlist。
func TestSortMappingNilRejected(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 1}
	db := newScriptedDB(t, state)
	if _, err := Paginate[widget](db.Model(&widget{}), Params{}, WithSortMapping(nil)); err == nil {
		t.Fatal("WithSortMapping(nil) 应报错")
	}
}

// TestSortMappingDefensiveCopy Option 构造后修改原 map 不得影响分页行为。
func TestSortMappingDefensiveCopy(t *testing.T) {
	t.Parallel()

	m := map[string]string{"created": "created_at"}
	opt := WithSortMapping(m)
	m["created"] = "hacked;column" // 构造后篡改
	m["evil"] = "1"

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	if _, err := Paginate[widget](db.Model(&widget{}), Params{Sort: "created"}, opt); err != nil {
		t.Fatalf("防御性复制后篡改不得影响行为: %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if !strings.Contains(dq, "`created_at`") || strings.Contains(dq, "hacked") {
		t.Fatalf("应使用构造时的映射快照: %q", dq)
	}
}

// ---------------------------------------------------------------------------
// H. 受限投影（DISTINCT / GROUP BY）契约
// ---------------------------------------------------------------------------

// TestRestrictedProjectionRequiresExplicitSortAndTotal 受限投影下的收窄契约：
// 必须显式提供排序列与总数；排序列不加表限定、不追加主键 tiebreaker。
func TestRestrictedProjectionRequiresExplicitSortAndTotal(t *testing.T) {
	t.Parallel()

	restrict := []struct {
		name  string
		apply func(*gorm.DB) *gorm.DB
	}{
		{name: "Distinct", apply: func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct("name") }},
		{name: "GroupBy", apply: func(db *gorm.DB) *gorm.DB {
			return db.Model(&widget{}).Select("name, COUNT(*) AS c").Group("name")
		}},
	}

	for _, r := range restrict {
		t.Run(r.name+"/缺排序列报错", func(t *testing.T) {
			t.Parallel()
			assertRestrictedError(t, r.apply, Params{}, ErrSortRequired)
		})
		t.Run(r.name+"/有排序即可自动统计", func(t *testing.T) {
			t.Parallel()
			assertRestrictedSuccess(t, r.apply)
		})
	}
}

// TestDistinctCountReliabilityBoundary GORM 只在「选择列表恰好一项且能切成单个字段」时
// 生成 count(DISTINCT col)，否则退化为 count(*) 返回总行数。多列 Distinct 若被当作可靠，
// 总数会静默错报（实测 25 行 / 15 个去重组合返回 25），故必须要求 WithTotal。
func TestDistinctCountReliabilityBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		apply    func(*gorm.DB) *gorm.DB
		reliable bool
	}{
		{
			name:     "单列 Distinct",
			apply:    func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct("name") },
			reliable: true,
		},
		{
			name:     "单列 Distinct 带表限定",
			apply:    func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct("widgets.name") },
			reliable: true,
		},
		{
			name:  "多列 Distinct",
			apply: func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct("name", "id") },
		},
		{
			name:  "Distinct 无参 + 多列 Select",
			apply: func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct().Select("name, id") },
		},
		{
			name:  "Distinct 无参无 Select",
			apply: func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct() },
		},
		{
			name:  "单列 Distinct 带引号（保守判为不可靠）",
			apply: func(db *gorm.DB) *gorm.DB { return db.Model(&widget{}).Distinct("`name`") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.reliable {
				assertRestrictedError(t, tt.apply, Params{Sort: "name", PageSize: 5}, ErrTotalRequired)

				// 显式提供总数即可放行
				state := &scriptState{total: 15, rows: []widget{{1, "a"}}}
				db := newScriptedDB(t, state)
				got, err := Paginate[widget](tt.apply(db),
					Params{Sort: "name", PageSize: 10}, WithTotal(15))
				if err != nil {
					t.Fatalf("WithTotal 后应放行: %v", err)
				}
				if got.TotalCount != 15 || got.TotalPage != 2 {
					t.Fatalf("total=%d pages=%d, want 15/2", got.TotalCount, got.TotalPage)
				}

				return
			}
			assertRestrictedSuccess(t, tt.apply)
		})
	}
}

// assertRestrictedError 受限投影契约不满足时：返回预期 sentinel 且不发出任何 SQL。
func assertRestrictedError(t *testing.T, apply func(*gorm.DB) *gorm.DB, params Params, want error) {
	t.Helper()
	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[widget](apply(db), params); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if queries, _ := state.recorded(); len(queries) != 0 {
		t.Fatalf("契约不满足时不应发出 SQL（尤其不得发 count）: %v", queries)
	}
}

// assertRestrictedSuccess 受限投影提供显式排序后：排序列不限定、不追加 tiebreaker，
// 且 Count 语义可靠的形式（链式 Distinct / GROUP BY）走自动统计。
func assertRestrictedSuccess(t *testing.T, apply func(*gorm.DB) *gorm.DB) {
	t.Helper()
	state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	got, err := Paginate[widget](apply(db), Params{Sort: "name", PageSize: 10})
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	if got.TotalCount != 5 || got.TotalPage != 1 {
		t.Fatalf("total=%d pages=%d, want 5/1", got.TotalCount, got.TotalPage)
	}
	dq, _ := dataSQL(queries(t, state))
	if orderByPart(dq) != "ORDER BY `name`" {
		t.Fatalf("受限投影排序列不得加表限定、不得追加 tiebreaker: %q", dq)
	}
}

// queries 读出已记录的 SQL 列表。
func queries(t *testing.T, state *scriptState) []string {
	t.Helper()
	qs, _ := state.recorded()

	return qs
}

// TestReservedWordColumnIsQuoted 保留字列名必须经方言引擎加引号，不拼原始 SQL。
func TestReservedWordColumnIsQuoted(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	if _, err := Paginate[widget](db.Model(&reservedWord{}), Params{Sort: "order", Order: "desc"}); err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `reserved_words`.`order` DESC,`reserved_words`.`id` DESC" {
		t.Fatalf("保留字列必须加引号: %q", dq)
	}
}

// TestUnparseableModelSurfacesRootCause 模型不可解析时透传根因，
// 而不是伪装成 ErrNoSortColumn；有显式排序时仍可工作（无 tiebreaker）。
func TestUnparseableModelSurfacesRootCause(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)

	_, err := Paginate[int](db, Params{})
	if err == nil {
		t.Fatal("不可解析的模型应报错")
	}
	if !strings.Contains(err.Error(), "parse model") || errors.Is(err, ErrNoSortColumn) {
		t.Fatalf("应透传 Parse 根因而非伪装成 ErrNoSortColumn: %v", err)
	}

	// 显式排序列下仍可分页（无 schema 故不限定、不追加 tiebreaker）
	state2 := &scriptState{total: 3, rows: []widget{{1, "a"}}, singleCol: true}
	db2 := newScriptedDB(t, state2)
	if _, sortedErr := Paginate[int](db2.Table("legacy"), Params{}, WithDefaultSort("id")); sortedErr != nil {
		t.Fatalf("显式排序下不可解析模型应可分页: %v", sortedErr)
	}
	queries, _ := state2.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `id`" {
		t.Fatalf("无 schema 时不得限定表名: %q", dq)
	}
}

// TestStructuredSelectDistinctDetected Distinct 也可能藏在结构化 SELECT 子句里
// （Clauses(clause.Select{Distinct: true})），此时 Statement.Distinct 为 false。
func TestStructuredSelectDistinctDetected(t *testing.T) {
	t.Parallel()

	state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	handle := db.Model(&widget{}).Clauses(clause.Select{
		Distinct: true, Columns: []clause.Column{{Name: "name"}},
	})

	if _, err := Paginate[widget](handle, Params{PageSize: 5}); !errors.Is(err, ErrSortRequired) {
		t.Fatalf("err = %v, want ErrSortRequired", err)
	}
	if qs := queries(t, state); len(qs) != 0 {
		t.Fatalf("不应发出 SQL: %v", qs)
	}

	// Statement.Distinct 为 false 的去重写法会让 Count 失去去重语义 → 要求 WithTotal
	if _, err := Paginate[widget](handle, Params{PageSize: 5, Sort: "name"}); !errors.Is(err, ErrTotalRequired) {
		t.Fatalf("err = %v, want ErrTotalRequired", err)
	}
	if _, err := Paginate[widget](handle, Params{PageSize: 5, Sort: "name"}, WithTotal(5)); err != nil {
		t.Fatalf("提供 WithTotal 后应可用: %v", err)
	}

	// DISTINCTROW 是 MySQL 里 DISTINCT 的同义词，同样要识别
	for _, sel := range []string{"DISTINCT name", "DISTINCTROW name", "distinct(name)"} {
		st := &scriptState{total: 5, rows: []widget{{1, "a"}}}
		h := newScriptedDB(t, st).Model(&widget{}).Select(sel)
		if _, err := Paginate[widget](h, Params{PageSize: 5}); !errors.Is(err, ErrSortRequired) {
			t.Fatalf("Select(%q): err = %v, want ErrSortRequired", sel, err)
		}
	}
	// 前缀相同但不是关键字的列名不得误判
	for _, sel := range []string{"distinct_id", "distinctness"} {
		st := &scriptState{total: 5, rows: []widget{{1, "a"}}}
		h := newScriptedDB(t, st).Model(&widget{}).Select(sel)
		if _, err := Paginate[widget](h, Params{PageSize: 5}); err != nil {
			t.Fatalf("Select(%q) 不应被判为去重投影: %v", sel, err)
		}
	}

	// 结构化外壳套原始串：合并后只剩内层 Expr，同样要按去重写法识别
	// （否则会生成 ORDER BY 不在选择列表中的 SQL，被 MySQL 以 Error 3065 拒绝）
	rawState := &scriptState{total: 5, rows: []widget{{1, "a"}}}
	rawDB := newScriptedDB(t, rawState)
	rawHandle := rawDB.Model(&widget{}).Clauses(clause.Select{
		Expression: clause.Expr{SQL: "DISTINCT name"},
	})
	if _, err := Paginate[widget](rawHandle, Params{PageSize: 5}); !errors.Is(err, ErrSortRequired) {
		t.Fatalf("err = %v, want ErrSortRequired", err)
	}
	if _, err := Paginate[widget](rawHandle, Params{PageSize: 5, Sort: "name"}); !errors.Is(err, ErrTotalRequired) {
		t.Fatalf("err = %v, want ErrTotalRequired", err)
	}
	if qs := queries(t, rawState); len(qs) != 0 {
		t.Fatalf("不应发出 SQL: %v", qs)
	}
}

// TestQualifierTableForms 表名限定的判定矩阵：取不准时宁可不限定，
// 也不能生成 FROM 里不存在的限定名（那会让每页都报 Error 1054）。
func TestQualifierTableForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		table string
		want  string
	}{
		{name: "JOIN表达式不限定", table: "widgets AS w JOIN others o ON o.wid = w.id", want: "ORDER BY `name`,`id`"},
		{name: "AS别名用别名", table: "widgets AS w", want: "ORDER BY `w`.`name`,`w`.`id`"},
		{name: "空格别名用别名", table: "widgets w", want: "ORDER BY `w`.`name`,`w`.`id`"},
		{name: "单表引用用表名", table: "widgets", want: "ORDER BY `widgets`.`name`,`widgets`.`id`"},
		{name: "库名限定用表名", table: "appdb.widgets", want: "ORDER BY `widgets`.`name`,`widgets`.`id`"},
		{name: "反引号表名用表名", table: "`widgets`", want: "ORDER BY `widgets`.`name`,`widgets`.`id`"},
		{name: "子查询别名用别名", table: "(SELECT * FROM widgets) AS w", want: "ORDER BY `w`.`name`,`w`.`id`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
			db := newScriptedDB(t, state)

			if _, err := Paginate[widget](db.Table(tt.table), Params{PageSize: 5, Sort: "name"}); err != nil {
				t.Fatalf("Paginate() error = %v", err)
			}
			queries, _ := state.recorded()
			dq, _ := dataSQL(queries)
			if orderByPart(dq) != tt.want {
				t.Fatalf("Table(%q) → %q, want %q", tt.table, orderByPart(dq), tt.want)
			}
		})
	}
}

// TestRemoteSortRestrictedToModelFields 未配置映射时，远端 Params.Sort 必须是本模型字段：
// 否则任意 a.b 都会直达数据库，稳定制造 500 并从错误文本回显库/表/列是否存在。
func TestRemoteSortRestrictedToModelFields(t *testing.T) {
	t.Parallel()

	for _, sort := range []string{"others.id", "mysql.user", "no_such_col", "widgets.no_such_col"} {
		state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
		db := newScriptedDB(t, state)

		if _, err := Paginate[widget](db.Model(&widget{}), Params{PageSize: 5, Sort: sort}); err != nil {
			t.Fatalf("Paginate(sort=%q) error = %v", sort, err)
		}
		queries, _ := state.recorded()
		dq, _ := dataSQL(queries)
		if orderByPart(dq) != "ORDER BY `widgets`.`id`" {
			t.Fatalf("sort=%q 应回退默认排序, got %q", sort, orderByPart(dq))
		}
	}

	// 开发者配置的列不受此限制（受信任）
	state := &scriptState{total: 5, rows: []widget{{1, "a"}}}
	db := newScriptedDB(t, state)
	if _, err := Paginate[widget](db.Model(&widget{}), Params{PageSize: 5, Sort: "k"},
		WithSortMapping(map[string]string{"k": "others.id"})); err != nil {
		t.Fatalf("映射值应受信任: %v", err)
	}
	queries, _ := state.recorded()
	dq, _ := dataSQL(queries)
	if orderByPart(dq) != "ORDER BY `others`.`id`,`widgets`.`id`" {
		t.Fatalf("映射到关联表列应被采用: %q", orderByPart(dq))
	}
}

// TestClampPagesPlatformBounds 32 位平台的两道上界保护在 64 位机上无法被触发，
// 故把上界作为参数注入并用手算常量断言，让分支真正被执行。
func TestClampPagesPlatformBounds(t *testing.T) {
	t.Parallel()

	const max32 = int64(math.MaxInt32) // 2147483647

	tests := []struct {
		name             string
		pages, size, max int64
		want             int64
	}{
		{name: "未触发上界", pages: 100, size: 10, max: max32, want: 100},
		{name: "offset上界钳制", pages: max32, size: 10, max: max32, want: max32/10 + 1},
		{name: "size=1不溢出", pages: max32, size: 1, max: max32, want: max32},
		{name: "页数上界钳制", pages: max32 + 5, size: 1, max: max32, want: max32},
		{name: "边界值恰好不钳", pages: max32/10 + 1, size: 10, max: max32, want: max32/10 + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := clampPages(tt.pages, tt.size, tt.max); got != tt.want {
				t.Errorf("clampPages(%d,%d,%d) = %d, want %d", tt.pages, tt.size, tt.max, got, tt.want)
			}
			// 钳制后的末页 offset 不得溢出上界
			if got := clampPages(tt.pages, tt.size, tt.max); (got-1)*tt.size > tt.max {
				t.Errorf("钳制后末页 offset (%d-1)*%d 超过上界 %d", got, tt.size, tt.max)
			}
		})
	}
}
