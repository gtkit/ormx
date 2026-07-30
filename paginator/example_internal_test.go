package paginator

import (
	"database/sql"
	"fmt"
	"strings"

	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// exampleDB 返回接了脚本化 fake driver 的句柄。
//
// Example 走完整的公开 API（Paginate 真实发出 count 与数据两条 SQL），
// 再打印 driver 记录下来的 SQL——示例展示的就是实际行为，不会与实现脱钩。
func exampleDB(state *scriptState) *gorm.DB {
	sqlDB := sql.OpenDB(&scriptConnector{state: state})
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		panic(err)
	}

	return db
}

// ExamplePaginate 展示常规分页：分页元信息由本包计算，
// 排序列与主键 tiebreaker 带表名限定并由方言引擎加引号，
// LIMIT/OFFSET 作为参数下发；调用方的 WHERE 条件原样保留。
func ExamplePaginate() {
	state := &scriptState{total: 42, rows: []widget{{1, "a"}, {2, "b"}}}
	db := exampleDB(state)

	page, err := Paginate[widget](
		db.Model(&widget{}).Where("name <> ?", "x"),
		Params{Page: 2, PageSize: 20, Sort: "name", Order: "desc"},
	)
	if err != nil {
		fmt.Println("err:", err)

		return
	}

	fmt.Println(page.CurrentPage, page.PageSize, page.TotalPage, page.TotalCount, len(page.Items))
	recorded, _ := state.recorded()
	for _, q := range recorded {
		fmt.Println(strings.TrimSpace(q))
	}
	// Output:
	// 2 20 3 42 2
	// SELECT count(*) FROM `widgets` WHERE name <> ?
	// SELECT * FROM `widgets` WHERE name <> ? ORDER BY `widgets`.`name` DESC,`widgets`.`id` DESC LIMIT ? OFFSET ?
}

// ExampleWithSortMapping 展示排序键映射：外部键 created 映射到受信任列 created_at
// （映射值由开发者配置，可为关联表列或别名，故不做表名限定也不追加主键次级排序之外的校验）；
// 未在映射中的键一律回退默认排序（模型主键），不接受任意列名。
func ExampleWithSortMapping() {
	state := &scriptState{total: 1, rows: []widget{{1, "a"}}}
	db := exampleDB(state)
	mapping := WithSortMapping(map[string]string{"created": "created_at"})

	for _, sort := range []string{"created", "name"} {
		if _, err := Paginate[widget](db.Model(&widget{}),
			Params{PageSize: 10, Sort: sort}, mapping); err != nil {
			fmt.Println("err:", err)

			return
		}
	}

	recorded, _ := state.recorded()
	for _, q := range recorded {
		if part := orderByPart(q); part != "" {
			fmt.Println(part)
		}
	}
	// Output:
	// ORDER BY `created_at`,`widgets`.`id`
	// ORDER BY `widgets`.`id`
}

// ExamplePaginate_restrictedProjection 展示受限投影（此处为 GROUP BY）的收窄契约：
// 必须显式提供排序列，否则返回 ErrSortRequired 且不发出任何 SQL；
// 提供后本包不追加主键次级排序（与投影的兼容性由调用方保证），总数自动统计为分组数量。
func ExamplePaginate_restrictedProjection() {
	state := &scriptState{total: 3, rows: []widget{{1, "a"}}}
	db := exampleDB(state)
	grouped := func() *gorm.DB {
		return db.Model(&widget{}).Select("name, COUNT(*) AS c").Group("name")
	}

	_, err := Paginate[widget](grouped(), Params{PageSize: 10})
	fmt.Println("缺排序列:", err)

	page, err := Paginate[widget](grouped(), Params{PageSize: 10, Sort: "name"})
	if err != nil {
		fmt.Println("err:", err)

		return
	}
	fmt.Println("总数:", page.TotalCount)

	recorded, _ := state.recorded()
	fmt.Println(orderByPart(recorded[len(recorded)-1]))
	// Output:
	// 缺排序列: paginator: explicit sort column required for DISTINCT/GROUP BY queries (automatic primary-key sort is incompatible with the projection); set Params.Sort or WithDefaultSort
	// 总数: 3
	// ORDER BY `name`
}
