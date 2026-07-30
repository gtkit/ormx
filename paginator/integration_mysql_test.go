//go:build integration

package paginator

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// 真实 MySQL 集成测试：fake driver 只能验 SQL 形状，无法判定数据库是否接受。
// DISTINCT / ONLY_FULL_GROUP_BY / 保留字 / Join 同名列这几类问题，
// 只有真实数据库才能当裁判，故本文件是发布前置条件的一部分。
//
// 运行：
//
//	ORM_RUN_INTEGRATION=1 ORM_TEST_DSN='root:@tcp(127.0.0.1:3306)/' \
//	  go test -tags integration -race -count=1 ./paginator/
const (
	integrationRunEnv = "ORM_RUN_INTEGRATION"
	integrationDSNEnv = "ORM_TEST_DSN"
	// integrationRequireEnv 置 1 时，缺少运行条件不再 skip 而是 fail——
	// 发版守卫用它防止"集成测试被静默跳过却照常打 tag"。
	integrationRequireEnv = "ORM_REQUIRE_INTEGRATION"
	integrationSchema     = "ormx_paginator_it"
)

type itWidget struct {
	ID           int64    `gorm:"primaryKey"`
	Name         string   `gorm:"size:64;index"`
	NullableName *string  `gorm:"size:64"`
	Order        int      `gorm:"column:order"` // 保留字列名
	Notes        []itNote `gorm:"foreignKey:WidgetID"`
}

func (itWidget) TableName() string { return "it_widgets" }

type itOther struct {
	ID  int64 `gorm:"primaryKey"` // 与 it_widgets 同名主键，用于制造 Join 列歧义
	WID int64 `gorm:"column:wid"`
}

func (itOther) TableName() string { return "it_others" }

type itNote struct {
	ID       int64 `gorm:"primaryKey"`
	WidgetID int64
	Body     string `gorm:"size:64"`
}

func (itNote) TableName() string { return "it_notes" }

type itComposite struct {
	OrderID int64 `gorm:"primaryKey;autoIncrement:false"`
	ItemID  int64 `gorm:"primaryKey;autoIncrement:false"`
	Qty     int
}

func (itComposite) TableName() string { return "it_composites" }

// newIntegrationDB 建库、迁移、灌数据，返回 *gorm.DB；测试结束删库。
func newIntegrationDB(t *testing.T) *gorm.DB {
	t.Helper()

	required := os.Getenv(integrationRequireEnv) == "1"
	skipOrFail := func(format string, args ...any) {
		t.Helper()
		if required {
			t.Fatalf("%s=1 但运行条件缺失: "+format, append([]any{integrationRequireEnv}, args...)...)
		}
		t.Skipf(format, args...)
	}

	if os.Getenv(integrationRunEnv) != "1" {
		skipOrFail("set %s=1 to run integration tests", integrationRunEnv)

		return nil
	}
	rawDSN := os.Getenv(integrationDSNEnv)
	if rawDSN == "" {
		skipOrFail("set %s to a MySQL DSN without a fixed schema, e.g. root:@tcp(127.0.0.1:3306)/", integrationDSNEnv)

		return nil
	}

	cfg, err := mysqldriver.ParseDSN(rawDSN)
	if err != nil {
		t.Fatalf("ParseDSN(%s): %v", integrationDSNEnv, err)
	}

	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, err := gorm.Open(gormmysql.Open(adminCfg.FormatDSN()), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	if err := admin.Exec("DROP DATABASE IF EXISTS " + integrationSchema).Error; err != nil {
		t.Fatalf("DROP DATABASE: %v", err)
	}
	if err := admin.Exec("CREATE DATABASE " + integrationSchema).Error; err != nil {
		t.Fatalf("CREATE DATABASE: %v", err)
	}

	schemaCfg := *cfg
	schemaCfg.DBName = integrationSchema
	db, err := gorm.Open(gormmysql.Open(schemaCfg.FormatDSN()), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
		_ = admin.Exec("DROP DATABASE IF EXISTS " + integrationSchema).Error
		if sqlDB, err := admin.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := db.AutoMigrate(&itWidget{}, &itOther{}, &itNote{}, &itComposite{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	// 25 行 widgets：name 每 5 行重复一次（制造非唯一排序列），order 保留字列有值
	widgets := make([]itWidget, 0, 25)
	for i := 1; i <= 25; i++ {
		var nullableName *string
		if i != 25 {
			value := fmt.Sprintf("v%02d", i%5)
			nullableName = &value
		}
		widgets = append(widgets, itWidget{
			ID:           int64(i),
			Name:         fmt.Sprintf("n%02d", i%5),
			NullableName: nullableName,
			Order:        i % 3,
		})
	}
	if err := db.Create(&widgets).Error; err != nil {
		t.Fatalf("插入 widgets: %v", err)
	}
	others := make([]itOther, 0, 25)
	for i := 1; i <= 25; i++ {
		others = append(others, itOther{ID: int64(100 + i), WID: int64(i)})
	}
	if err := db.Create(&others).Error; err != nil {
		t.Fatalf("插入 others: %v", err)
	}
	notes := make([]itNote, 0, 25)
	for i := 1; i <= 25; i++ {
		notes = append(notes, itNote{ID: int64(200 + i), WidgetID: int64(i), Body: "note"})
	}
	if err := db.Create(&notes).Error; err != nil {
		t.Fatalf("插入 notes: %v", err)
	}
	composites := []itComposite{
		{OrderID: 1, ItemID: 1, Qty: 1},
		{OrderID: 1, ItemID: 2, Qty: 2},
		{OrderID: 2, ItemID: 1, Qty: 3},
		{OrderID: 2, ItemID: 2, Qty: 4},
		{OrderID: 3, ItemID: 1, Qty: 5},
	}
	if err := db.Create(&composites).Error; err != nil {
		t.Fatalf("插入 composites: %v", err)
	}

	return db
}

// TestIntegrationBasicPaginationNoDuplicateOrMiss 翻页正确性：
// 非唯一排序列（name 每 5 行重复）下逐页取全量，断言无重复、无遗漏。
func TestIntegrationBasicPaginationNoDuplicateOrMiss(t *testing.T) {
	db := newIntegrationDB(t)

	seen := make(map[int64]int)
	pageSize := 7
	firstPage, err := Paginate[itWidget](db.Model(&itWidget{}), Params{Page: 1, PageSize: pageSize, Sort: "name"})
	if err != nil {
		t.Fatalf("第 1 页: %v", err)
	}
	if firstPage.TotalCount != 25 || firstPage.TotalPage != 4 {
		t.Fatalf("total=%d pages=%d, want 25/4", firstPage.TotalCount, firstPage.TotalPage)
	}
	for _, w := range firstPage.Items {
		seen[w.ID]++
	}
	for p := 2; p <= firstPage.TotalPage; p++ {
		got, err := Paginate[itWidget](db.Model(&itWidget{}), Params{Page: p, PageSize: pageSize, Sort: "name"})
		if err != nil {
			t.Fatalf("第 %d 页: %v", p, err)
		}
		for _, w := range got.Items {
			seen[w.ID]++
		}
	}
	if len(seen) != 25 {
		t.Fatalf("翻页覆盖 %d 行, want 25（有遗漏）", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id=%d 出现 %d 次（重复）", id, n)
		}
	}
}

// TestIntegrationReservedWordColumn 保留字列名 order 必须被正确引用，
// 原始字符串拼接会产生 MySQL 语法错误。
func TestIntegrationReservedWordColumn(t *testing.T) {
	db := newIntegrationDB(t)

	got, err := Paginate[itWidget](db.Model(&itWidget{}), Params{PageSize: 5, Sort: "order", Order: "desc"})
	if err != nil {
		t.Fatalf("保留字列排序应被正确引用: %v", err)
	}
	if len(got.Items) != 5 {
		t.Fatalf("items=%d, want 5", len(got.Items))
	}

	// WithDefaultSort 与映射路径同样要经过引用
	if _, err := Paginate[itWidget](db.Model(&itWidget{}), Params{PageSize: 5}, WithDefaultSort("order")); err != nil {
		t.Fatalf("WithDefaultSort 保留字列: %v", err)
	}
	if _, err := Paginate[itWidget](db.Model(&itWidget{}), Params{PageSize: 5, Sort: "k"},
		WithSortMapping(map[string]string{"k": "order"})); err != nil {
		t.Fatalf("映射到保留字列: %v", err)
	}
}

// TestIntegrationJoinAmbiguousColumn Join 两表同名 id：
// 未限定的 ORDER BY id 会被 MySQL 判为歧义列，本包必须自动限定。
func TestIntegrationJoinAmbiguousColumn(t *testing.T) {
	db := newIntegrationDB(t)

	join := func() *gorm.DB {
		return db.Model(&itWidget{}).Joins("JOIN it_others ON it_others.wid = it_widgets.id")
	}

	// 默认排序（主键回退）
	if _, err := Paginate[itWidget](join(), Params{PageSize: 5}); err != nil {
		t.Fatalf("Join 默认排序应自动限定主键: %v", err)
	}
	// 显式未限定排序列 id：也必须限定
	if _, err := Paginate[itWidget](join(), Params{PageSize: 5, Sort: "id"}); err != nil {
		t.Fatalf("Join 显式未限定排序列应自动限定: %v", err)
	}
	// 非主键排序 + tiebreaker
	if _, err := Paginate[itWidget](join(), Params{PageSize: 5, Sort: "name"}); err != nil {
		t.Fatalf("Join 下 name 排序 + 主键 tiebreaker: %v", err)
	}
	// 调用方自行限定
	if _, err := Paginate[itWidget](join(), Params{PageSize: 5, Sort: "it_widgets.id"}); err != nil {
		t.Fatalf("Join 下调用方限定排序列: %v", err)
	}

	// 真正的歧义场景：选择列表同时含两表的 id（Model 查询会被 GORM 限定 SELECT，
	// 故必须显式展开两表列才能复现），此时未限定的 ORDER BY id 会被 MySQL 判为歧义。
	ambiguous := func() *gorm.DB {
		return db.Table("it_widgets").
			Select("it_widgets.*, it_others.*").
			Joins("JOIN it_others ON it_others.wid = it_widgets.id")
	}
	if _, err := Paginate[itWidget](ambiguous(), Params{PageSize: 5, Sort: "id"}); err != nil {
		t.Fatalf("选择列表歧义时排序列必须自动限定: %v", err)
	}
	if _, err := Paginate[itWidget](ambiguous(), Params{PageSize: 5, Sort: "name"}); err != nil {
		t.Fatalf("选择列表歧义时 tiebreaker 必须限定: %v", err)
	}
}

// TestIntegrationCompositePrimaryKey 复合主键：全集参与排序且被数据库接受。
func TestIntegrationCompositePrimaryKey(t *testing.T) {
	db := newIntegrationDB(t)

	got, err := Paginate[itComposite](db.Model(&itComposite{}), Params{PageSize: 2})
	if err != nil {
		t.Fatalf("复合主键分页: %v", err)
	}
	if got.TotalCount != 5 || got.TotalPage != 3 || len(got.Items) != 2 {
		t.Fatalf("total=%d pages=%d items=%d, want 5/3/2", got.TotalCount, got.TotalPage, len(got.Items))
	}
	if got.Items[0].OrderID != 1 || got.Items[0].ItemID != 1 {
		t.Fatalf("首行=%+v, want order_id=1 item_id=1（按复合主键升序）", got.Items[0])
	}
}

// TestIntegrationDistinctProjection DISTINCT 查询：
// 自动追加主键 tiebreaker 会被 MySQL 拒绝（ORDER BY 列不在选择列表），
// 因此受限投影必须要求显式排序且不追加 tiebreaker；总数必须显式提供。
func TestIntegrationDistinctProjection(t *testing.T) {
	db := newIntegrationDB(t)

	// 未提供显式排序：必须 fail fast，而不是生成被 MySQL 拒绝的 SQL
	if _, err := Paginate[itWidget](db.Model(&itWidget{}).Distinct("name"),
		Params{PageSize: 5}); err == nil {
		t.Fatal("DISTINCT 未提供显式排序应报错")
	}

	// 非 NULL 数据上 GORM Count 恰好与结果数一致，但分页器仍不得据此推断所有列可靠。
	var distinctNames int64
	if err := db.Model(&itWidget{}).Distinct("name").Count(&distinctNames).Error; err != nil {
		t.Fatalf("统计去重数: %v", err)
	}
	if distinctNames != 5 {
		t.Fatalf("GORM 对链式 Distinct 的 Count = %d, want 5", distinctNames)
	}

	if _, err := Paginate[itWidget](db.Model(&itWidget{}).Distinct("name"),
		Params{PageSize: 10, Sort: "name"}); !errors.Is(err, ErrTotalRequired) {
		t.Fatalf("err = %v, want ErrTotalRequired", err)
	}

	explicit, err := Paginate[itWidget](db.Model(&itWidget{}).Distinct("name"),
		Params{PageSize: 2, Sort: "name"}, WithTotal(distinctNames))
	if err != nil {
		t.Fatalf("DISTINCT + WithTotal: %v", err)
	}
	if explicit.TotalCount != 5 || explicit.TotalPage != 3 {
		t.Fatalf("total=%d pages=%d, want 5/3", explicit.TotalCount, explicit.TotalPage)
	}
}

// TestIntegrationNullableDistinctCountMismatch 证明单列 DISTINCT 也不能自动统计：
// SELECT DISTINCT 会保留一个 NULL，COUNT(DISTINCT col) 则排除 NULL。
func TestIntegrationNullableDistinctCountMismatch(t *testing.T) {
	db := newIntegrationDB(t)

	var count int64
	if err := db.Model(&itWidget{}).Distinct("nullable_name").Count(&count).Error; err != nil {
		t.Fatalf("COUNT(DISTINCT nullable_name): %v", err)
	}
	// 目标类型必须实现 sql.Scanner：GORM 的 Pluck 对 []*string 会解引用后按 *string 扫描，
	// 无法接收 NULL 行——而本用例的前提恰恰是 SELECT DISTINCT 会保留 NULL。
	var values []sql.NullString
	if err := db.Model(&itWidget{}).Distinct("nullable_name").Order("nullable_name").Pluck("nullable_name", &values).Error; err != nil {
		t.Fatalf("SELECT DISTINCT nullable_name: %v", err)
	}
	if count != 5 || len(values) != 6 {
		t.Fatalf("count=%d distinct rows=%d, want 5/6", count, len(values))
	}

	query := db.Model(&itWidget{}).Distinct("nullable_name")
	if _, err := Paginate[itWidget](query, Params{PageSize: 5, Sort: "nullable_name"}); !errors.Is(err, ErrTotalRequired) {
		t.Fatalf("err = %v, want ErrTotalRequired", err)
	}
	got, err := Paginate[itWidget](query, Params{Page: 2, PageSize: 5, Sort: "nullable_name"}, WithTotal(6))
	if err != nil {
		t.Fatalf("WithTotal: %v", err)
	}
	if got.TotalCount != 6 || got.TotalPage != 2 || len(got.Items) != 1 {
		t.Fatalf("total=%d pages=%d items=%d, want 6/2/1", got.TotalCount, got.TotalPage, len(got.Items))
	}
}

// TestIntegrationGroupByProjection GROUP BY 查询（sql_mode 含 ONLY_FULL_GROUP_BY）：
// 自动 tiebreaker 会被拒绝，故要求显式排序列；总数则可自动统计——GORM 对 GROUP BY
// 取统计查询的返回行数（*count = tx.RowsAffected），即分组数量。
func TestIntegrationGroupByProjection(t *testing.T) {
	db := newIntegrationDB(t)

	grouped := func() *gorm.DB {
		return db.Model(&itWidget{}).Select("name, SUM(`order`) AS s").Group("name")
	}

	// 未提供显式排序：fail fast
	if _, err := Paginate[itWidget](grouped(), Params{PageSize: 5}); err == nil {
		t.Fatal("GROUP BY 未提供显式排序应报错")
	}

	// 先证明前提：GORM 对 GROUP BY 的 Count 返回分组数量，而不是首组计数
	var groups int64
	if err := grouped().Count(&groups).Error; err != nil {
		t.Fatalf("GROUP BY 自动统计: %v", err)
	}
	if groups != 5 {
		t.Fatalf("GORM 对 GROUP BY 的 Count = %d, want 5（分组数量，自动统计前提不成立）", groups)
	}

	// 显式排序即可：总数自动统计为分组数量
	got, err := Paginate[itWidget](grouped(), Params{PageSize: 3, Sort: "name"})
	if err != nil {
		t.Fatalf("GROUP BY + 显式排序应被接受: %v", err)
	}
	if got.TotalCount != 5 || got.TotalPage != 2 || len(got.Items) != 3 {
		t.Fatalf("total=%d pages=%d items=%d, want 5/2/3", got.TotalCount, got.TotalPage, len(got.Items))
	}

	// 统计不得被分页子句截断：末页仍按总数算出正确页码与条数
	last, err := Paginate[itWidget](grouped(), Params{Page: 2, PageSize: 3, Sort: "name"})
	if err != nil {
		t.Fatalf("GROUP BY 末页: %v", err)
	}
	if last.CurrentPage != 2 || len(last.Items) != 2 {
		t.Fatalf("末页 page=%d items=%d, want 2/2", last.CurrentPage, len(last.Items))
	}
}

// TestIntegrationNestedScopeOverride 嵌套 scope 覆盖本包分页子句：真实数据库上
// count 查询被 LIMIT/OFFSET 截断后返回 0 行，GORM 取 RowsAffected 作总数 → 总数
// 静默为 0、翻页返回空页。必须报错而不是产出这种结果。
func TestIntegrationNestedScopeOverride(t *testing.T) {
	db := newIntegrationDB(t)

	nested := func(d *gorm.DB) *gorm.DB {
		return d.Scopes(func(x *gorm.DB) *gorm.DB {
			return x.Limit(2).Offset(20).Order("name DESC")
		})
	}

	got, err := Paginate[itWidget](db.Model(&itWidget{}).Scopes(nested), Params{Page: 1, PageSize: 5})
	if !errors.Is(err, ErrDeferredPaginationClause) {
		t.Fatalf("err = %v, want ErrDeferredPaginationClause（实测不报错时 total=%d items=%d）",
			err, got.TotalCount, len(got.Items))
	}

	// 对照：嵌套 scope 只加条件时必须正常工作
	cond, err := Paginate[itWidget](db.Model(&itWidget{}).Scopes(func(d *gorm.DB) *gorm.DB {
		return d.Scopes(func(x *gorm.DB) *gorm.DB { return x.Where("id <= ?", 10) })
	}), Params{PageSize: 4, Sort: "id"})
	if err != nil {
		t.Fatalf("纯条件嵌套 scope 应正常: %v", err)
	}
	if cond.TotalCount != 10 || len(cond.Items) != 4 {
		t.Fatalf("total=%d items=%d, want 10/4", cond.TotalCount, len(cond.Items))
	}
}

// TestIntegrationNestedScopeOrderExpression 嵌套 scope 用 clause.OrderBy.Expression
// 顶替排序：Build 会完全忽略 Columns，而 MergeClause 把本包的 Columns 原样复制过来，
// 只查 Columns 会被骗过。实测放行时生成 ORDER BY RAND()，OFFSET 分页重复且遗漏。
func TestIntegrationNestedScopeOrderExpression(t *testing.T) {
	db := newIntegrationDB(t)

	handle := db.Model(&itWidget{}).Scopes(func(d *gorm.DB) *gorm.DB {
		return d.Scopes(func(x *gorm.DB) *gorm.DB {
			return x.Clauses(clause.OrderBy{Expression: clause.Expr{SQL: "RAND()"}})
		})
	})
	got, err := Paginate[itWidget](handle, Params{Page: 1, PageSize: 5})
	if !errors.Is(err, ErrDeferredPaginationClause) {
		ids := make([]int64, 0, len(got.Items))
		for _, w := range got.Items {
			ids = append(ids, w.ID)
		}
		t.Fatalf("err = %v, want ErrDeferredPaginationClause（放行时行序不可控：ids=%v）", err, ids)
	}
}

// TestIntegrationScopesInjectedDistinctJoin scope 注入 Distinct + 一对多 JOIN：
// 数据查询去重、统计查询不去重，总数按 JOIN 后的重复行计数——实测 3 个实体报 6。
// 投影在构建前不可见，必须在执行后比对并拦下。
func TestIntegrationScopesInjectedDistinctJoin(t *testing.T) {
	db := newIntegrationDB(t)

	// 给 id<=3 的 widget 各补一条 note，使 JOIN 后行数翻倍
	if err := db.Exec(
		"INSERT INTO it_notes (id, widget_id, body) VALUES (901,1,'x'),(902,2,'x'),(903,3,'x')").Error; err != nil {
		t.Fatalf("插入 notes: %v", err)
	}

	handle := db.Model(&itWidget{}).
		Joins("JOIN it_notes n ON n.widget_id = it_widgets.id").
		Where("it_widgets.id <= ?", 3).
		Scopes(func(d *gorm.DB) *gorm.DB { return d.Distinct() })

	got, err := Paginate[itWidget](handle, Params{PageSize: 10, Sort: "id"})
	if !errors.Is(err, ErrDeferredPaginationClause) {
		t.Fatalf("err = %v, want ErrDeferredPaginationClause（放行时 total=%d 而实体只有 3 条）",
			err, got.TotalCount)
	}

	// 对照：把 Distinct 写在链式调用上并显式提供总数即受契约保护。
	chained := db.Model(&itWidget{}).
		Joins("JOIN it_notes n ON n.widget_id = it_widgets.id").
		Where("it_widgets.id <= ?", 3).
		Distinct("it_widgets.id")
	ok, err := Paginate[itWidget](chained, Params{PageSize: 10, Sort: "it_widgets.id"}, WithTotal(3))
	if err != nil {
		t.Fatalf("链式 Distinct 应被接受: %v", err)
	}
	if ok.TotalCount != 3 {
		t.Fatalf("total=%d, want 3", ok.TotalCount)
	}
}

// TestIntegrationMultiColumnDistinctTotal 多列 Distinct 的 GORM Count 退化为 count(*)：
// 返回总行数而非去重组合数。若当作可靠会静默错报总数并多出空页，必须要求 WithTotal。
func TestIntegrationMultiColumnDistinctTotal(t *testing.T) {
	db := newIntegrationDB(t)

	// 先证明前提：真实去重组合数与 GORM Count 的差异确实存在
	var combos, gormCount int64
	if err := db.Raw("SELECT COUNT(*) FROM (SELECT DISTINCT name, `order` FROM it_widgets) x").
		Scan(&combos).Error; err != nil {
		t.Fatalf("统计去重组合数: %v", err)
	}
	if err := db.Model(&itWidget{}).Distinct("name", "`order`").Count(&gormCount).Error; err != nil {
		t.Fatalf("GORM Count: %v", err)
	}
	if combos != 15 || gormCount != 25 {
		t.Fatalf("去重组合=%d GORM Count=%d, want 15/25（前提变了，判定需重新评估）", combos, gormCount)
	}

	// 多列 Distinct：必须 fail fast，不得用 25 当总数
	if _, err := Paginate[itWidget](db.Model(&itWidget{}).Distinct("name", "`order`"),
		Params{PageSize: 10, Sort: "name"}); !errors.Is(err, ErrTotalRequired) {
		t.Fatalf("err = %v, want ErrTotalRequired", err)
	}

	// 显式提供真实去重数后放行，页数按 15 算（不再多出空页）
	got, err := Paginate[itWidget](db.Model(&itWidget{}).Distinct("name", "`order`"),
		Params{Page: 2, PageSize: 10, Sort: "name"}, WithTotal(combos))
	if err != nil {
		t.Fatalf("WithTotal 后应放行: %v", err)
	}
	if got.TotalCount != 15 || got.TotalPage != 2 || len(got.Items) != 5 {
		t.Fatalf("total=%d pages=%d items=%d, want 15/2/5", got.TotalCount, got.TotalPage, len(got.Items))
	}
}

// TestIntegrationScopesInjectedGroupBy Scopes 注入的 GROUP BY 在构建前不可见，
// 本包按普通查询处理（会追加与分组冲突的主键次级排序、统计语义也随之改变）。
// 执行后的投影比对必须把它拦下并给出指向根因的错误，而不是让调用方去猜数据库的
// Error 1055，更不能静默产出结果。文档要求把 Group 写在链式调用上。
func TestIntegrationScopesInjectedGroupBy(t *testing.T) {
	db := newIntegrationDB(t)

	scoped := func() *gorm.DB {
		return db.Model(&itWidget{}).Select("name, SUM(`order`) AS s").
			Scopes(func(d *gorm.DB) *gorm.DB { return d.Group("name") })
	}

	for _, tc := range []struct {
		name   string
		params Params
		opts   []Option
	}{
		{"缺显式排序", Params{PageSize: 5}, nil},
		{"有排序仍追加主键 tiebreaker", Params{PageSize: 5, Sort: "name"}, nil},
		{"显式总数也绕不过", Params{PageSize: 5, Sort: "name"}, []Option{WithTotal(5)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Paginate[itWidget](scoped(), tc.params, tc.opts...)
			if !errors.Is(err, ErrDeferredPaginationClause) {
				t.Fatalf("err = %v, want ErrDeferredPaginationClause", err)
			}
		})
	}

	// 对照：同样的查询写成链式 Group 即受契约保护，可自动统计分组数
	chained := db.Model(&itWidget{}).Select("name, SUM(`order`) AS s").Group("name")
	got, err := Paginate[itWidget](chained, Params{PageSize: 3, Sort: "name"})
	if err != nil {
		t.Fatalf("链式 Group 应被接受: %v", err)
	}
	if got.TotalCount != 5 || got.TotalPage != 2 || len(got.Items) != 3 {
		t.Fatalf("total=%d pages=%d items=%d, want 5/2/3", got.TotalCount, got.TotalPage, len(got.Items))
	}
}

// TestIntegrationTableAlias 表别名：schema 表名在别名查询里不可用于 ORDER BY，
// 本包不得强行限定（应保持调用方给的列引用）。
func TestIntegrationTableAlias(t *testing.T) {
	db := newIntegrationDB(t)

	// 别名查询 + 调用方限定到别名
	aliased := db.Table("it_widgets AS w").Select("w.id, w.name, w.`order`")
	got, err := Paginate[itWidget](aliased, Params{PageSize: 5, Sort: "w.id"})
	if err != nil {
		t.Fatalf("表别名 + 别名限定排序: %v", err)
	}
	if len(got.Items) != 5 || got.TotalCount != 25 {
		t.Fatalf("items=%d total=%d, want 5/25", len(got.Items), got.TotalCount)
	}

	// 别名查询 + 未限定排序列：不得被限定成 schema 表名（否则 MySQL 报未知列）
	if _, err := Paginate[itWidget](db.Table("it_widgets AS w"), Params{PageSize: 5, Sort: "name"}); err != nil {
		t.Fatalf("表别名下未限定排序列不得被强行限定: %v", err)
	}

	// 省略 AS 的空格别名形式（GORM 同样会提取别名）
	if _, err := Paginate[itWidget](db.Table("it_widgets w"), Params{PageSize: 5, Sort: "name"}); err != nil {
		t.Fatalf("空格别名形式: %v", err)
	}
	if _, err := Paginate[itWidget](db.Table("it_widgets w"), Params{PageSize: 5, Sort: "w.id"}); err != nil {
		t.Fatalf("空格别名 + 别名限定排序列: %v", err)
	}

	// 库名限定形式 db.table：GORM 将 Statement.Table 置为表名，限定后仍可解析
	if _, err := Paginate[itWidget](db.Table(integrationSchema+".it_widgets"),
		Params{PageSize: 5, Sort: "name"}); err != nil {
		t.Fatalf("库名限定表形式: %v", err)
	}
}

// TestIntegrationCountIsolatedFromCallerPagination 真实库上验证 Count 隔离：
// 入参句柄残留的 Limit/Offset 不得影响总数（否则总数会被截断）。
func TestIntegrationCountIsolatedFromCallerPagination(t *testing.T) {
	db := newIntegrationDB(t)

	// 调用方残留 Limit(3).Offset(20)：总数必须仍是 25
	got, err := Paginate[itWidget](db.Model(&itWidget{}).Limit(3).Offset(20),
		Params{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if got.TotalCount != 25 {
		t.Fatalf("TotalCount=%d, want 25（Count 继承了调用方分页子句）", got.TotalCount)
	}
	if len(got.Items) != 10 {
		t.Fatalf("items=%d, want 10（数据查询继承了调用方 Limit）", len(got.Items))
	}

	// 调用方残留 ORDER BY 也不得影响排序（由本包全权决定）
	got2, err := Paginate[itWidget](db.Model(&itWidget{}).Order("name DESC"),
		Params{Page: 1, PageSize: 3, Sort: "id"})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if len(got2.Items) != 3 || got2.Items[0].ID != 1 {
		t.Fatalf("首行=%+v，want id=1（应按本包的 id 升序，而非调用方的 name DESC）", got2.Items)
	}
}

// TestIntegrationPreloadAndWhere 常规路径回归：Where + Preload 查询装配不受影响
// （README 与 Example 首推的用法，必须有真实库覆盖）。
func TestIntegrationPreloadAndWhere(t *testing.T) {
	db := newIntegrationDB(t)

	// Preload 关联必须正常加载
	preloaded, err := Paginate[itWidget](db.Model(&itWidget{}).Preload("Notes"),
		Params{PageSize: 5, Sort: "id"})
	if err != nil {
		t.Fatalf("Preload + 分页: %v", err)
	}
	if len(preloaded.Items) != 5 || preloaded.TotalCount != 25 {
		t.Fatalf("items=%d total=%d, want 5/25", len(preloaded.Items), preloaded.TotalCount)
	}
	for _, w := range preloaded.Items {
		if len(w.Notes) != 1 {
			t.Fatalf("widget %d 的 Notes 未被预加载: %+v", w.ID, w.Notes)
		}
	}

	// 带条件预加载同样有效
	filtered, err := Paginate[itWidget](db.Model(&itWidget{}).Preload("Notes", "body = ?", "none"),
		Params{PageSize: 3, Sort: "id"})
	if err != nil {
		t.Fatalf("带条件 Preload: %v", err)
	}
	for _, w := range filtered.Items {
		if len(w.Notes) != 0 {
			t.Fatalf("条件不匹配时不应加载关联: %+v", w.Notes)
		}
	}

	got, err := Paginate[itWidget](db.Model(&itWidget{}).Where("`order` = ?", 1),
		Params{PageSize: 4, Sort: "id", Order: "desc"})
	if err != nil {
		t.Fatalf("Where + 分页: %v", err)
	}
	if got.TotalCount == 0 || len(got.Items) == 0 {
		t.Fatalf("Where 过滤后应有数据: total=%d items=%d", got.TotalCount, len(got.Items))
	}
	for i := 1; i < len(got.Items); i++ {
		if got.Items[i-1].ID < got.Items[i].ID {
			t.Fatalf("desc 排序未生效: %v", got.Items)
		}
	}
}

// TestIntegrationGuardNoFalsePositive 三子句事后校验的误报面：常见良性查询形态
// （事务、行锁、Session 派生、Preload、Joins、软删除语义、直接注入 clause.Limit、
// 页码越界、单层 scope 分页）都不得被误判成 ErrDeferredPaginationClause。
func TestIntegrationGuardNoFalsePositive(t *testing.T) {
	db := newIntegrationDB(t)

	tests := []struct {
		name   string
		handle func() *gorm.DB
		params Params
	}{
		{
			name: "事务内句柄",
			handle: func() *gorm.DB {
				tx := db.Begin()
				// 必须回滚：未结束的事务持有表元数据锁，会让清理阶段的 DROP DATABASE 永久阻塞
				t.Cleanup(func() { tx.Rollback() })

				return tx.Model(&itWidget{})
			},
			params: Params{PageSize: 5, Sort: "id"},
		},
		{
			name:   "行锁",
			handle: func() *gorm.DB { return db.Model(&itWidget{}).Clauses(clause.Locking{Strength: "SHARE"}) },
			params: Params{PageSize: 5, Sort: "id"},
		},
		{
			name:   "Session 派生",
			handle: func() *gorm.DB { return db.Model(&itWidget{}).Session(&gorm.Session{}) },
			params: Params{PageSize: 5, Sort: "id"},
		},
		{
			name:   "Preload",
			handle: func() *gorm.DB { return db.Model(&itWidget{}).Preload("Notes") },
			params: Params{PageSize: 5, Sort: "id"},
		},
		{
			name: "Joins",
			handle: func() *gorm.DB {
				return db.Model(&itWidget{}).Joins("JOIN it_others o ON o.wid = it_widgets.id")
			},
			params: Params{PageSize: 5, Sort: "id"},
		},
		{
			name: "直接注入 clause.Limit",
			handle: func() *gorm.DB {
				return db.Model(&itWidget{}).Clauses(clause.Limit{Offset: 7})
			},
			params: Params{PageSize: 5, Sort: "id"},
		},
		{
			name:   "页码越界（钳制到末页）",
			handle: func() *gorm.DB { return db.Model(&itWidget{}) },
			params: Params{Page: 999, PageSize: 10, Sort: "id"},
		},
		{
			name: "单层 scope 注入分页",
			handle: func() *gorm.DB {
				return db.Model(&itWidget{}).Scopes(func(d *gorm.DB) *gorm.DB {
					return d.Limit(2).Offset(20).Order("name DESC")
				})
			},
			params: Params{Page: 1, PageSize: 10},
		},
		{
			name: "多层嵌套但只加条件",
			handle: func() *gorm.DB {
				return db.Model(&itWidget{}).Scopes(func(d *gorm.DB) *gorm.DB {
					return d.Scopes(func(x *gorm.DB) *gorm.DB {
						return x.Scopes(func(y *gorm.DB) *gorm.DB { return y.Where("id > ?", 0) })
					})
				})
			},
			params: Params{PageSize: 5, Sort: "id"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Paginate[itWidget](tt.handle(), tt.params)
			if err != nil {
				t.Fatalf("良性形态被拦下: %v", err)
			}
			if got.TotalCount != 25 {
				t.Fatalf("total=%d, want 25", got.TotalCount)
			}
			if len(got.Items) == 0 {
				t.Fatalf("items 为空: %+v", got)
			}
		})
	}
}

// TestIntegrationCallerHandleReusableAfterPaginate 真实库上验证句柄零污染：
// 分页后复用同一句柄做普通查询，不得残留 ORDER/LIMIT/OFFSET。
func TestIntegrationCallerHandleReusableAfterPaginate(t *testing.T) {
	db := newIntegrationDB(t)

	caller := db.Model(&itWidget{}).Where("id <= ?", 20)
	if _, err := Paginate[itWidget](caller, Params{Page: 2, PageSize: 5}); err != nil {
		t.Fatalf("Paginate: %v", err)
	}

	var all []itWidget
	if err := caller.Find(&all).Error; err != nil {
		t.Fatalf("复用句柄: %v", err)
	}
	if len(all) != 20 {
		t.Fatalf("复用句柄返回 %d 行, want 20（分页子句残留）", len(all))
	}
}

// TestIntegrationScopesInjectedPagination Scopes 注入分页/排序子句（GORM 官方文档
// Scopes 章节的分页写法）：本包的三子句以尾随 scope 下发，必须后发制人覆盖它们——
// 统计不被 LIMIT/OFFSET 截断，排序不被追加成次级排序。
func TestIntegrationScopesInjectedPagination(t *testing.T) {
	db := newIntegrationDB(t)

	scopePaginate := func(page, size int) func(*gorm.DB) *gorm.DB {
		return func(d *gorm.DB) *gorm.DB { return d.Offset((page - 1) * size).Limit(size) }
	}

	// scope 的 LIMIT 5 / OFFSET 5 必须被覆盖：总数为全表 25、首页取 10 条且从 id=1 开始
	got, err := Paginate[itWidget](db.Model(&itWidget{}).Scopes(scopePaginate(2, 5)),
		Params{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Scopes 注入分页子句应被覆盖而非报错: %v", err)
	}
	if got.TotalCount != 25 || got.TotalPage != 3 || len(got.Items) != 10 {
		t.Fatalf("total=%d pages=%d items=%d, want 25/3/10", got.TotalCount, got.TotalPage, len(got.Items))
	}
	if got.Items[0].ID != 1 {
		t.Fatalf("首页首条 id=%d, want 1（scope 的 OFFSET 未被归零）", got.Items[0].ID)
	}

	// scope 的 ORDER BY 必须被丢弃（Reorder），排序只由 Params 决定
	desc, err := Paginate[itWidget](db.Model(&itWidget{}).Scopes(func(d *gorm.DB) *gorm.DB {
		return d.Order("name DESC")
	}), Params{PageSize: 5, Sort: "id"})
	if err != nil {
		t.Fatalf("Scopes 注入排序子句应被覆盖而非报错: %v", err)
	}
	if desc.Items[0].ID != 1 {
		t.Fatalf("首条 id=%d, want 1（scope 的 ORDER BY 未被丢弃）", desc.Items[0].ID)
	}

	// 第 2 页同样按本包的 offset 走，不受 scope 影响
	p2, err := Paginate[itWidget](db.Model(&itWidget{}).Scopes(scopePaginate(2, 5)),
		Params{Page: 2, PageSize: 10})
	if err != nil {
		t.Fatalf("第 2 页: %v", err)
	}
	if len(p2.Items) != 10 || p2.Items[0].ID != 11 {
		t.Fatalf("第 2 页 items=%d 首条 id=%d, want 10/11", len(p2.Items), p2.Items[0].ID)
	}

	// 纯条件 scope 必须正常工作
	cond, err := Paginate[itWidget](db.Model(&itWidget{}).Scopes(func(d *gorm.DB) *gorm.DB {
		return d.Where("id <= ?", 10)
	}), Params{PageSize: 4, Sort: "id"})
	if err != nil {
		t.Fatalf("纯条件 scope 应正常: %v", err)
	}
	if cond.TotalCount != 10 || len(cond.Items) != 4 {
		t.Fatalf("total=%d items=%d, want 10/4", cond.TotalCount, len(cond.Items))
	}
}

// TestIntegrationTableExpressionWithJoin Table 表达式含 JOIN 时 GORM 提取不出别名，
// 若回退 schema 表名做限定会让每页都报 Error 1054。
func TestIntegrationTableExpressionWithJoin(t *testing.T) {
	db := newIntegrationDB(t)

	handle := func() *gorm.DB {
		return db.Table("it_widgets AS w JOIN it_others o ON o.wid = w.id").Select("w.id, w.name")
	}
	if _, err := Paginate[itWidget](handle(), Params{PageSize: 5, Sort: "name"}); err != nil {
		t.Fatalf("JOIN 表达式 + 显式排序: %v", err)
	}
	if _, err := Paginate[itWidget](handle(), Params{PageSize: 5}); err != nil {
		t.Fatalf("JOIN 表达式 + 默认主键排序: %v", err)
	}
}

// TestIntegrationStructuredSelectDistinct clause.Select{Distinct} 是结构化写法，
// Statement.Distinct 为 false，必须同样被识别为受限投影。
func TestIntegrationStructuredSelectDistinct(t *testing.T) {
	db := newIntegrationDB(t)

	handle := func() *gorm.DB {
		return db.Model(&itWidget{}).Clauses(clause.Select{
			Distinct: true, Columns: []clause.Column{{Name: "name"}},
		})
	}
	if _, err := Paginate[itWidget](handle(), Params{PageSize: 5}); err == nil {
		t.Fatal("clause.Select{Distinct} 缺显式排序应报错，而不是生成被拒绝的 SQL")
	}
	got, err := Paginate[itWidget](handle(), Params{PageSize: 10, Sort: "name"}, WithTotal(5))
	if err != nil {
		t.Fatalf("clause.Select{Distinct} + 排序 + WithTotal 应被接受: %v", err)
	}
	if len(got.Items) != 5 {
		t.Fatalf("items=%d, want 5", len(got.Items))
	}
}

// TestIntegrationSQLModeGuard 守卫：本套测试的价值依赖 ONLY_FULL_GROUP_BY 生效，
// 若目标库未启用则显式失败，避免测试通过给出虚假信心。
func TestIntegrationSQLModeGuard(t *testing.T) {
	db := newIntegrationDB(t)

	var mode string
	if err := db.Raw("SELECT @@sql_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("读取 sql_mode: %v", err)
	}
	if !strings.Contains(strings.ToUpper(mode), "ONLY_FULL_GROUP_BY") {
		t.Fatalf("目标库未启用 ONLY_FULL_GROUP_BY，GROUP BY 相关断言失去意义: %s", mode)
	}
}
