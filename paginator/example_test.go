package paginator_test

import (
	"context"
	"fmt"

	"github.com/gtkit/ormx"
	"github.com/gtkit/ormx/paginator"
)

type Comment struct {
	ID      int64
	TopicID int64
	Status  string
}

type Topic struct {
	ID         int64
	CategoryID int64
	Title      string
	Comments   []Comment // 预加载在查询句柄上自行完成，本包不代理
}

// ExamplePaginate 展示典型分页查询：查询条件（含预加载）在句柄上组装完成后
// 交给 Paginate 执行；排序键经显式映射（线上推荐），分页三子句由本包全权负责。
//
// 本示例依赖真实 MySQL，无法在测试环境执行，因此不带 // Output:（编译级校验）。
func ExamplePaginate() {
	client, err := ormx.Open(context.Background(),
		ormx.WithHost("127.0.0.1"),
		ormx.WithPort("3306"),
		ormx.WithDatabase("app"),
		ormx.WithUser("root"),
		ormx.WithPassword("secret"),
	)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer client.Close()

	query := client.DB().WithContext(context.Background()).
		Model(&Topic{}).
		Where("category_id = ?", 42).
		Preload("Comments", "status = ?", "published") // 预加载属查询装配，直接链式

	page, err := paginator.Paginate[Topic](query,
		paginator.Params{Page: 2, PageSize: 20, Sort: "created", Order: "desc"},
		paginator.WithSortMapping(map[string]string{"created": "created_at"}), // 外部键→受信任列
		paginator.WithMaxPageSize(50),
	)
	if err != nil {
		fmt.Println("paginate:", err)
		return
	}

	fmt.Println(page.CurrentPage, page.TotalPage, page.TotalCount, len(page.Items))
}
