// Package paginator 提供基于 GORM 的通用分页查询执行器。
//
// 契约：
//   - Paginate 全权负责 ORDER BY / LIMIT / OFFSET 三个子句——入参句柄上残留的
//     同类子句会被清除（分页与排序只由 Params 与 Option 决定），其余条件
//     （WHERE/Joins/Select 等）原样保留；这三个子句以追加在最后的 scope 形式下发，
//     因此 Scopes 里写的排序与分页（GORM 官方文档正把分页列为 Scopes 的典型用法）
//     会被本包覆盖，而不会反过来截断统计或破坏排序。唯一的例外是 scope 内部再注册
//     scope（组合式 helper）：GORM 把它排到下一轮、也就是本包之后，此时本包在语句
//     执行后按事实校验并返回 [ErrDeferredPaginationClause]，不静默产出错误页；
//   - 入参句柄零污染：内部经独立 Session 工作，调用后原句柄可安全复用；
//     多个 goroutine 并发把同一句柄传给 Paginate 也是安全的。但该句柄同时被用于
//     其它查询（Find/First/Count 等）不安全——那是 GORM 句柄语义所限，
//     请各 goroutine 自行 Session/WithContext 派生；
//   - 排序经 GORM 的 clause.OrderBy 结构化下发，列名由方言引擎加引号——
//     保留字列（如 order）可安全排序，不拼接原始 SQL；
//   - 排序列与主键列在表名可判定时自动加表名限定（防 Join 场景列歧义）：
//     纯 Model 查询、Table("t")/Table("db.t")/Table("t AS a")/Table("t a") 均可判定；
//     Table 表达式含 JOIN、子查询、多表时取不准，一律不限定（此时 Join 列歧义
//     需调用方自行限定排序列）；
//   - 排序稳定性：自动追加全部主键列（含复合主键）作为次级排序，方向跟随主排序——
//     在数据集不变且排序键组合唯一时翻页不重不漏（模型不可解析而由调用方显式指定
//     排序时无法追加主键；OFFSET 分页跨请求增删时仍可能重/漏，见使用限制）；
//   - PageSize 有上限（默认 100，WithMaxPageSize 调整）——该参数常来自远端请求，
//     必须钳制以防无界查询；任意配置值下均不 panic、分页元信息不溢出；
//   - 入参句柄携带的 db.Error 在入口即被包装返回，任何路径都不会静默成功；
//   - 未配置 [WithSortMapping] 时，Params.Sort 作为远端输入只接受本模型字段
//     （跨表引用与未知列一律回退默认排序，避免任意 a.b 直达数据库制造错误并
//     从错误文本回显库/表/列是否存在）；WithSortMapping 的映射值与
//     [WithDefaultSort] 由开发者配置，视为受信任列，可为关联表列或查询别名；
//   - Items 恒非 nil（空页序列化为 [] 而非 null）。
//
// 受限投影（DISTINCT / GROUP BY）：
//
// 这两类查询的投影与分组决定了哪些列可出现在 ORDER BY（MySQL 的 DISTINCT 约束与
// ONLY_FULL_GROUP_BY），自动主键排序会被数据库拒绝。本包不猜测调用方的投影，
// 对可见的受限投影收窄契约：必须经 Params.Sort（配合 WithSortMapping）或
// WithDefaultSort 显式提供排序列，否则返回 [ErrSortRequired]；该列不做表名限定、
// 也不追加主键次级排序，与投影的兼容性由调用方保证。
//
// 统计（Count）语义按去重写法区分，以真实 MySQL 8 实测为准：
//   - 单列链式 Distinct（Statement.Distinct 为 true 且选择列表恰好一个合法列引用）：
//     GORM 生成 count(DISTINCT col)，去重数准确，可自动统计；
//   - GROUP BY（链式 Group 或 clause.GroupBy）：GORM 取统计查询的返回行数作为总数
//     （见 gorm finisher_api.go 中 *count = tx.RowsAffected），即分组数量，可自动统计。
//     代价是每个分组返回一行：统计开销为 O(分组数) 的网络传输与扫描，
//     高基数分组（几十万以上）请改用 [WithTotal] 自行统计；
//   - 多列链式 Distinct、Clauses(clause.Select{Distinct: true})、原始字符串
//     Select("DISTINCT ...")：GORM 一律退化为 count(*)，返回总行数而非去重数
//     （实测 Distinct("name", "`order`") 在 25 行 / 15 个去重组合上返回 25），
//     必须经 [WithTotal] 提供总数，否则返回 [ErrTotalRequired]。
//
// 受限投影的识别只看构建前可见的状态：链式 Distinct/Group、clause.Select{Distinct}、
// clause.GroupBy、原始字符串里的 DISTINCT/DISTINCTROW 前缀。写在 Scopes 里的投影修饰在执行阶段
// 才物化、识别不到，本包按普通查询处理，结果一律是响亮失败而非静默错误：
// Group 触发 ONLY_FULL_GROUP_BY 拒绝（Error 1055），Distinct 与多列 Select 触发
// Count 的扫描/类型错误（错误文本由 GORM 与 driver 给出，指向性较差）。
// 请把 Distinct/Group/Select 写在链式调用上。
//
// 其他使用限制（如实声明）：
//   - 非受限投影下 Count 遵循 GORM Count 语义：入参含 Select/Joins 时"总数"含义
//     随之变化（如 COUNT(col) 跳过 NULL、has-many Join 重复计数）；不符合需求时用
//     [WithTotal] 自行提供总数；
//   - Count 与数据查询是两条 SQL，非一致性快照；严格一致场景请传入事务内句柄；
//   - [WithTotal] 传入的总数不与实际数据核对：值不准（缓存陈旧、算错）时会静默
//     产出错误的页数与空页，准确性由调用方负责；
//   - 行为仅在 MySQL 8 上做过真实数据库验证（见 integration_mysql_test.go）。
//     其他方言的引号与 DISTINCT/GROUP BY 宽容度不同（如 SQLite 对 ORDER BY 不在
//     选择列表中更宽容，Postgres 加引号后大小写敏感），使用前请自行验证；
//   - 仅 OFFSET 分页，深分页（大数据量 × 大页码）不适合高频接口，
//     此类场景应采用游标分页（不属于本包 API）。
//
// 典型用法：
//
//	query := client.DB().WithContext(ctx).Model(&Topic{}).Where("category_id = ?", cid)
//	page, err := paginator.Paginate[Topic](query, paginator.Params{Page: 2, PageSize: 20},
//	    paginator.WithSortMapping(map[string]string{"created": "created_at"}), // 线上推荐显式映射
//	)
package paginator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultPageSize    = 10
	defaultMaxPageSize = 100
	// maxColumnSegments 列引用最多两段：column 或 table.column。
	maxColumnSegments = 2

	orderByClauseName = "ORDER BY"
	groupByClauseName = "GROUP BY"
	selectClauseName  = "SELECT"
	limitClauseName   = "LIMIT"
	orderDesc         = "desc"
	orderAsc          = "asc"
	// noLimit 是 GORM 里「不发 LIMIT」的哨兵值（clause.Limit 只在 >= 0 时输出）。
	noLimit = -1
)

// ErrNilDB 表示传入的 *gorm.DB 为 nil。
var ErrNilDB = errors.New("paginator: nil db")

// ErrNoSortColumn 表示模型没有主键、调用方也未提供排序列，无法确定排序。
var ErrNoSortColumn = errors.New(
	"paginator: no sort column (model has no primary key; set Params.Sort or WithDefaultSort)")

// ErrSortRequired 表示 DISTINCT / GROUP BY 查询未提供显式排序列。
// 这类查询的可排序列由投影与分组决定，自动主键排序会被数据库拒绝，
// 故必须经 Params.Sort（配合 WithSortMapping）或 WithDefaultSort 指定。
var ErrSortRequired = errors.New(
	"paginator: explicit sort column required for DISTINCT/GROUP BY queries " +
		"(automatic primary-key sort is incompatible with the projection); set Params.Sort or WithDefaultSort")

// ErrTotalRequired 表示当前查询的去重形式会让 GORM 的 Count 失去去重语义，
// 必须经 WithTotal 提供总数。出现在这几类写法上：多列链式 Distinct、
// 原始字符串 Select("DISTINCT ...")、Clauses(clause.Select{Distinct: true})——
// 此时 GORM 的 Count 退化为 count(*)，返回的是总行数而非去重数（已在真实 MySQL 实测）。
// 单列链式 Distinct 与 GROUP BY 的 Count 语义可靠，不需要本项。
var ErrTotalRequired = errors.New(
	"paginator: WithTotal required for this DISTINCT form (GORM Count loses the DISTINCT semantics " +
		"and returns the row count); use a single-column .Distinct(col) instead, or compute the total explicitly")

// ErrDeferredPaginationClause 表示嵌套 scope 覆盖了本包的分页子句。
//
// GORM 的执行入口是「只要还有 scope 就再跑一轮」的多轮循环，而 scope 内部再注册的
// scope（组合式 scope helper 的常见写法）会排到下一轮——也就是本包尾随 scope 之后，
// 从而覆盖 LIMIT/OFFSET：统计被截断则总数静默为 0、翻页静默返回空页。
// gorm.Statement.scopes 未导出，无法在包外排空，故本包在语句执行后按事实校验，
// 不一致即报错，绝不静默产出错误结果。
// 请把排序与分页经 Params / Option 传入，不要写在（嵌套的）scope 里。
var ErrDeferredPaginationClause = errors.New(
	"paginator: ORDER BY/LIMIT/OFFSET overridden by a nested scope (a scope that registers another scope " +
		"runs after this package's trailing scope) and would silently corrupt the page; " +
		"pass sorting/pagination via Params/Option instead")

// Params 分页请求参数（常绑定自远端请求）：
//   - Page：页码，从 1 开始；越界自动钳制到 [1, 总页数]。
//   - PageSize：每页条数；<=0 取默认值 10，上限见 WithMaxPageSize（默认 100）。
//   - Sort：排序键。设置了 WithSortMapping 时仅接受映射键（未命中回退默认排序）；
//     未设置映射时作为列名经文法校验（点分 1~2 段、每段合法标识符），非法回退默认排序。
//   - Order：asc/desc（不区分大小写），其余值回退 asc。
type Params struct {
	Page     int    `form:"page"      json:"page"`
	PageSize int    `form:"page_size" json:"pageSize"`
	Sort     string `form:"sort"      json:"sort"`
	Order    string `form:"order"     json:"order"`
}

// Page 分页查询结果。
//
// Items 恒非 nil；无匹配数据时 TotalPage 与 CurrentPage 均为 0。
type Page[T any] struct {
	Items       []T   `json:"items"`       // 当前页数据
	CurrentPage int   `json:"currentPage"` // 实际生效的页码（经钳制）
	PageSize    int   `json:"pageSize"`    // 实际生效的每页条数（经钳制）
	TotalPage   int   `json:"totalPage"`   // 总页数
	TotalCount  int64 `json:"totalCount"`  // 总条数
}

type settings struct {
	maxPageSize int
	defaultSort string
	sortMapping map[string]string
	total       *int64
}

// Option 配置分页器自身（不修改查询语义；预加载等查询装配请直接在入参句柄上完成）。
// nil Option 被安全跳过。
type Option func(*settings) error

// WithMaxPageSize 设置 PageSize 上限（默认 100）；n 必须为正。
// 该上限本身是信任边界配置：调大即接受对应规模的全量查询开销——
// 库保证任意 n 下不 panic、分页元信息不溢出，但查询代价由调用方承担。
func WithMaxPageSize(n int) Option {
	return func(s *settings) error {
		if n <= 0 {
			return fmt.Errorf("paginator: max page size must be positive, got %d", n)
		}
		s.maxPageSize = n

		return nil
	}
}

// WithDefaultSort 覆盖默认排序列（默认为模型主键）；列名经文法校验。
func WithDefaultSort(column string) Option {
	return func(s *settings) error {
		if !isSafeColumn(column) {
			return fmt.Errorf("paginator: invalid default sort column %q", column)
		}
		s.defaultSort = column

		return nil
	}
}

// WithSortMapping 设置「外部排序键 → 受信任列」的显式映射（线上推荐形态）：
// 设置后 Params.Sort 仅接受映射键，未命中一律回退默认排序，不再接受任意列名——
// 既防注入，也防调用方对任意合法列发起无索引大排序。映射值经列名文法校验。
//
// m 不得为 nil（nil 几乎必是 bug，且会静默关闭 allowlist；零键 allowlist 请传空 map）。
// 构造时即做防御性复制：Option 创建后调用方修改原 map 不影响分页行为。
func WithSortMapping(m map[string]string) Option {
	if m == nil {
		return func(*settings) error {
			return errors.New("paginator: sort mapping must not be nil (use an empty map for a zero-key allowlist)")
		}
	}

	cloned := maps.Clone(m)
	var invalid error
	for k, col := range cloned {
		if !isSafeColumn(col) {
			invalid = fmt.Errorf("paginator: sort mapping %q -> %q: invalid column", k, col)

			break
		}
	}

	return func(s *settings) error {
		if invalid != nil {
			return invalid
		}
		s.sortMapping = cloned

		return nil
	}
}

// WithTotal 由调用方提供总数并跳过 count 查询：适用于总数已缓存、count 过重，
// 或默认 Count 语义不符合需求的场景；DISTINCT / GROUP BY 查询下为必需项。
// total 不得为负。
func WithTotal(total int64) Option {
	return func(s *settings) error {
		if total < 0 {
			return fmt.Errorf("paginator: total must be non-negative, got %d", total)
		}
		s.total = &total

		return nil
	}
}

// Paginate 执行分页查询。
//
// db 为已组装查询条件的句柄（未显式 Model/Table 时按 T 推导）；超时与取消经
// db.WithContext 由调用方控制。入参句柄不会被修改，调用后可安全复用。
func Paginate[T any](db *gorm.DB, params Params, opts ...Option) (Page[T], error) {
	if db == nil {
		return Page[T]{}, ErrNilDB
	}
	if db.Error != nil {
		// 入参句柄已携带错误：任何路径（含 WithTotal 短路）都不得吞掉它
		return Page[T]{}, fmt.Errorf("paginator: input query error: %w", db.Error)
	}

	s := settings{maxPageSize: defaultMaxPageSize}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&s); err != nil {
			return Page[T]{}, err
		}
	}

	// 私有化 Statement 后再做任何读写：入参句柄零污染，且并发把同一句柄传进来时
	// 不会互相写（schemaOf 里的 Parse 会写 Statement.Schema/Table）。
	tx := privateSession(db)
	if tx.Statement.Model == nil {
		tx = tx.Model(new(T))
	}

	proj := projectionOf(tx)

	order, err := buildOrder(tx, params, &s, proj.restricted)
	if err != nil {
		return Page[T]{}, err
	}

	pageSize := clampPageSize(params.PageSize, s.maxPageSize)

	total, err := resolveTotal(tx, &s, proj)
	if err != nil {
		return Page[T]{}, err
	}

	totalPage := calcTotalPage(total, pageSize)
	page := clampPage(params.Page, totalPage)
	result := Page[T]{
		Items:       []T{},
		CurrentPage: page,
		PageSize:    pageSize,
		TotalPage:   totalPage,
		TotalCount:  total,
	}
	if total == 0 {
		return result, nil
	}

	// 不按 pageSize 预分配：容量来自可配输入，极值会 panic 或吃内存（Find 自增长即可）
	items := []T{}
	offset := (page - 1) * pageSize
	res := tx.Session(&gorm.Session{}).
		Scopes(applyPaginationScope(order, pageSize, offset)).
		Find(&items)
	if !paginationIntact(res.Statement, pageSize, offset) || !orderIntact(res.Statement, order) {
		if res.Error != nil {
			return Page[T]{}, fmt.Errorf("%w: query page: %w", ErrDeferredPaginationClause, res.Error)
		}

		return Page[T]{}, ErrDeferredPaginationClause
	}
	if res.Error != nil {
		return Page[T]{}, fmt.Errorf("paginator: query page: %w", res.Error)
	}
	result.Items = items

	return result, nil
}

// privateSession 派生持有独立 Statement 的句柄。
//
// db.Session(&gorm.Session{}) 只把 clone 置为 2——Statement 指针仍与入参句柄共享，
// 要到下一次 finisher/条件方法才克隆。本包在那之前就要读子句、还要 Parse 模型
// （Parse 会写 Statement.Schema/Table），共享指针会让并发传同一句柄的调用方互相写。
// 传入非 nil Context 可让 Session 立即克隆 Statement（见 gorm.go 的 Session 实现），
// 是不依赖内部字段就能私有化的唯一手段。
func privateSession(db *gorm.DB) *gorm.DB {
	ctx := context.Background()
	if db.Statement != nil && db.Statement.Context != nil {
		ctx = db.Statement.Context
	}

	return db.Session(&gorm.Session{Context: ctx})
}

// resolveTotal 决定总条数：WithTotal 优先；Count 语义不可靠的去重写法必须显式提供；
// 其余走 GORM Count——经尾随 scope 在调用方 scope 之后清除排序与分页子句，
// 保证统计不被截断。
func resolveTotal(tx *gorm.DB, s *settings, proj projection) (int64, error) {
	if s.total != nil {
		return *s.total, nil
	}
	if proj.unreliableCount {
		return 0, ErrTotalRequired
	}

	var total int64
	res := tx.Session(&gorm.Session{}).Scopes(stripPaginationScope).Count(&total)
	// 先判分页子句：被嵌套 scope 截断时 count 查询要么返回 0 行（总数静默为 0）、
	// 要么直接被数据库拒绝（如只注入 OFFSET 时的 Error 1064）——两种都以本包的
	// 哨兵为主错误，避免调用方只看到指向 GORM/driver 内部的文本而找不到根因。
	if !paginationIntact(res.Statement, noLimit, 0) {
		if res.Error != nil {
			return 0, fmt.Errorf("%w: count total: %w", ErrDeferredPaginationClause, res.Error)
		}

		return 0, ErrDeferredPaginationClause
	}
	if res.Error != nil {
		return 0, fmt.Errorf("paginator: count total: %w", res.Error)
	}

	return total, nil
}

// paginationIntact 校验语句执行后的 LIMIT/OFFSET 仍是本包设置的值。
//
// 只看事实（执行完的 Statement），不做预测：不发额外 SQL、不重跑调用方 scope，
// 因此没有探针那类可观察副作用。
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

// orderIntact 校验本包的排序列仍完整地排在最前。
//
// 嵌套 scope 追加的排序列排在本包之后（不影响行序，本包的主键次级排序已让顺序全序），
// 故只要前缀仍是本包设置的列即视为完好；用 Reorder 截断本包排序的写法会被这里拦下。
func orderIntact(stmt *gorm.Statement, want clause.OrderBy) bool {
	c, ok := stmt.Clauses[orderByClauseName]
	if !ok {
		return false
	}
	got, isOrderBy := c.Expression.(clause.OrderBy)
	if !isOrderBy || len(got.Columns) < len(want.Columns) {
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
func applyPaginationScope(order clause.OrderBy, pageSize, offset int) func(*gorm.DB) *gorm.DB {
	return func(d *gorm.DB) *gorm.DB {
		return d.Clauses(order).Limit(pageSize).Offset(noLimit).Offset(offset)
	}
}

// stripPaginationScope 是统计用的尾随 scope：在调用方 scope 之后清除排序与分页，
// 避免 Count 被 LIMIT/OFFSET 截断（截断后 count 查询返回 0 行 → 总数静默为 0）。
func stripPaginationScope(d *gorm.DB) *gorm.DB {
	delete(d.Statement.Clauses, orderByClauseName)

	return d.Limit(noLimit).Offset(noLimit)
}

// buildOrder 构建结构化 ORDER BY 子句。
//
// 优先级：SortMapping 命中 >（无映射时）属于本模型字段的 Params.Sort > WithDefaultSort > 模型主键。
// 首列带 Reorder：合并时丢弃调用方（含其 scope）的排序列，使排序只由本包决定。
// 受限投影下必须命中前三者之一，且不做表名限定、不追加主键次级排序。
func buildOrder(tx *gorm.DB, params Params, s *settings, restricted bool) (clause.OrderBy, error) {
	desc := normalizeOrder(params.Order) == orderDesc
	info, parseErr := schemaOf(tx)

	// 排序列的信任分级：
	//   映射值与 WithDefaultSort 由开发者配置，视为受信任列（可为关联表列或别名）；
	//   未配置映射时的 Params.Sort 来自远端，必须是本模型字段——否则任意 a.b
	//   都会直达数据库，稳定制造错误并从错误文本回显库/表/列是否存在。
	explicit := ""
	if s.sortMapping != nil {
		explicit = s.sortMapping[params.Sort] // 未命中即空 → 回退默认
	} else if info.allowsRemoteSort(params.Sort) {
		explicit = params.Sort
	}
	if explicit == "" {
		explicit = s.defaultSort
	}

	if restricted {
		if explicit == "" {
			return clause.OrderBy{}, ErrSortRequired
		}
		// 投影兼容性由调用方保证：不限定表名、不追加主键
		col := schemaInfo{}.column(explicit, desc)
		col.Reorder = true

		return clause.OrderBy{Columns: []clause.OrderByColumn{col}}, nil
	}

	sortCol := explicit
	if sortCol == "" {
		if parseErr != nil {
			// 透传根因：模型无法解析（如基础类型/map）时，调用方需显式指定排序列
			return clause.OrderBy{}, parseErr
		}
		if len(info.pkCols) == 0 {
			return clause.OrderBy{}, ErrNoSortColumn
		}
		sortCol = info.pkCols[0]
	}

	columns := make([]clause.OrderByColumn, 0, len(info.pkCols)+1)
	primary := info.column(sortCol, desc)
	primary.Reorder = true
	columns = append(columns, primary)
	for _, pk := range info.pkCols {
		if info.sameColumn(sortCol, pk) {
			continue
		}
		columns = append(columns, info.column(pk, desc))
	}

	return clause.OrderBy{Columns: columns}, nil
}

// projection 描述查询投影对分页的两处影响。
type projection struct {
	// restricted 投影或分组限定了可出现在 ORDER BY 的列：
	// 自动主键次级排序会被数据库拒绝，故要求调用方显式提供排序列。
	restricted bool
	// unreliableCount GORM 的 Count 在该写法下失去去重语义，必须由调用方提供总数。
	unreliableCount bool
}

// projectionOf 判定查询投影（构建前可见的形式）。各分支的 Count 可靠性均在
// 真实 MySQL 上实测过：
//   - 链式 Distinct（单列 / 多列 / 裸）：Count 生成 COUNT(DISTINCT ...)，可靠；
//   - GROUP BY：Count 返回分组数，可靠；
//   - Clauses(clause.Select{Distinct}) 与原始字符串 Select("DISTINCT ..."):
//     Statement.Distinct 为 false，Count 退化为 count(*) 返回总行数，不可靠。
//
// Scopes 等延迟机制注入的去重/分组在此看不到（它们在执行阶段才物化）。
// 后果是自动追加的主键次级排序与投影冲突，被数据库明确拒绝
// （实测 MySQL Error 1055 / 3065）——是响亮失败而非静默错误结果。
func projectionOf(tx *gorm.DB) projection {
	if tx.Statement.Distinct {
		return projection{restricted: true, unreliableCount: !distinctCountIsReliable(tx.Statement.Selects)}
	}
	if c, ok := tx.Statement.Clauses[selectClauseName]; ok {
		switch expr := c.Expression.(type) {
		case clause.Select:
			if expr.Distinct {
				return projection{restricted: true, unreliableCount: true}
			}
		case clause.Expr:
			// clause.Select{Expression: clause.Expr{SQL: "DISTINCT …"}}：结构化外壳套原始串，
			// 合并后只剩内层 Expr（Select.MergeClause 的行为），故按原始串判定
			if hasDistinctPrefix(expr.SQL) {
				return projection{restricted: true, unreliableCount: true}
			}
		}
	}
	if rawDistinctSelect(tx.Statement.Selects) {
		return projection{restricted: true, unreliableCount: true}
	}
	if _, grouped := tx.Statement.Clauses[groupByClauseName]; grouped {
		return projection{restricted: true}
	}

	return projection{}
}

// distinctCountIsReliable 判断链式 Distinct 下 GORM 的 Count 是否保留去重语义。
//
// GORM 只在「选择列表恰好一项、且能切成单个字段（或 col AS alias / t.col 三段）」时
// 生成 count(DISTINCT col)，否则一律退化为 count(*)——返回总行数而非去重数
// （实测 Distinct("name", "`order`") 在 25 行 / 15 个去重组合上返回 25）。
// 本包用更严的判定（单项且为合法列引用），是 GORM 条件的子集：
// 只会把可靠的判成不可靠（要求 WithTotal），不会反过来放行错误总数。
func distinctCountIsReliable(selects []string) bool {
	return len(selects) == 1 && isSafeColumn(selects[0])
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
	lower := strings.ToLower(strings.TrimSpace(sql))
	for _, kw := range [...]string{"distinctrow", "distinct"} {
		if !strings.HasPrefix(lower, kw) {
			continue
		}
		rest := lower[len(kw):]
		if rest == "" || rest[0] == ' ' || rest[0] == '\t' || rest[0] == '(' {
			return true
		}
	}

	return false
}

// schemaInfo 承载排序所需的模型元信息：可用于限定的表名、主键列与全部字段列名。
type schemaInfo struct {
	table  string // 空表示不可限定（表名非纯标识符，如含别名表达式）
	pkCols []string
	fields map[string]struct{}
}

// schemaOf 解析模型 schema；解析失败时返回包装后的根因错误（不伪装成排序错误）。
func schemaOf(tx *gorm.DB) (schemaInfo, error) {
	if tx.Statement.Model == nil {
		return schemaInfo{}, nil
	}
	if err := tx.Statement.Parse(tx.Statement.Model); err != nil {
		return schemaInfo{}, fmt.Errorf("paginator: parse model: %w", err)
	}
	sch := tx.Statement.Schema
	if sch == nil {
		return schemaInfo{}, nil
	}

	info := schemaInfo{fields: make(map[string]struct{}, len(sch.FieldsByDBName))}
	for dbName := range sch.FieldsByDBName {
		info.fields[dbName] = struct{}{}
	}
	for _, f := range sch.PrimaryFields {
		if f != nil && f.DBName != "" {
			info.pkCols = append(info.pkCols, f.DBName)
		}
	}

	info.table = qualifierTable(tx, sch.Table)

	return info, nil
}

// qualifierTable 决定可用于列限定的表名，取不准就返回空串（宁可不限定，
// 也不能生成 FROM 里不存在的限定名——那会让每一页都报 MySQL Error 1054）。
//
//   - 调用方未用 Table 表达式（纯 Model 或 Table("plain")）：用 Statement.Table
//     或 schema 表名，二者都与 FROM 一致；
//   - 用了 Table 表达式且 GORM 提取出的名字与 schema 表名不同：说明提取到的是
//     别名（Table("t AS a") / Table("t a")），别名正是 FROM 里的限定名，可用；
//   - 用了 Table 表达式而提取结果等于 schema 表名或非标识符：GORM 只是回退到了
//     基表名，它在 FROM 里可能并不存在（如 Table("t AS a JOIN o ON ...") 提取出 "t"，
//     而 FROM 用的是别名 a）——不可判定，不限定。
func qualifierTable(tx *gorm.DB, schemaTable string) string {
	table := tx.Statement.Table

	if tx.Statement.TableExpr == nil {
		if table == "" {
			table = schemaTable
		}
		if isIdentifier(table) {
			return table
		}

		return ""
	}

	// GORM 从表达式里提取出的名字与 schema 表名不同 → 提取到的是别名，可用于限定
	if table != schemaTable && isIdentifier(table) {
		return table
	}
	// 表达式本身只是单个表引用（可带库名与引号）→ 限定名与 FROM 一致，可用
	if isIdentifier(table) && bareTableExpr(tx.Statement.TableExpr, table) {
		return table
	}

	return ""
}

// bareTableExpr 判断 Table 表达式是否只是单个表引用（如 widgets、`widgets`、
// appdb.widgets）——此时用表名限定列与 FROM 一致；含空格、括号或逗号
// （JOIN、子查询、多表）则不可判定。
func bareTableExpr(expr clause.Expression, table string) bool {
	raw, ok := expr.(*clause.Expr)
	if !ok || table == "" {
		return false
	}
	normalized := strings.NewReplacer("`", "", `"`, "").Replace(raw.SQL)
	normalized = strings.TrimSpace(normalized)
	if strings.ContainsAny(normalized, " \t\n(),") {
		return false
	}

	return normalized == table || strings.HasSuffix(normalized, "."+table)
}

// column 把列引用构建为结构化排序列：调用方已限定的原样拆分；
// 未限定且该列属于模型字段时补表名限定（防 Join 列歧义），
// 别名与计算列（不在模型字段中）保持未限定，避免误加前缀。
func (i schemaInfo) column(raw string, desc bool) clause.OrderByColumn {
	table, name := splitColumn(raw)
	if table == "" && i.table != "" {
		if _, ok := i.fields[name]; ok {
			table = i.table
		}
	}

	return clause.OrderByColumn{
		Column: clause.Column{Table: table, Name: name},
		Desc:   desc,
	}
}

// allowsRemoteSort 判断远端提供的排序键是否可信：文法合法、是本模型字段，
// 且未限定或限定到本模型表（"others.id" 这类跨表引用一律拒绝，回退默认排序）。
func (i schemaInfo) allowsRemoteSort(raw string) bool {
	if !isSafeColumn(raw) {
		return false
	}
	table, name := splitColumn(raw)
	if _, ok := i.fields[name]; !ok {
		return false
	}

	return table == "" || table == i.table
}

// sameColumn 判断排序列是否就是该主键列：末段列名须一致，
// 且排序列未限定或限定到同一张表（"others.id" 与本表主键 "id" 不是同一列）。
func (i schemaInfo) sameColumn(sortCol, pkDBName string) bool {
	table, name := splitColumn(sortCol)
	if name != pkDBName {
		return false
	}

	return table == "" || table == i.table
}

// splitColumn 拆分列引用；isSafeColumn 已限制最多两段，故取最后一个点即可。
func splitColumn(raw string) (table, name string) {
	if i := strings.LastIndexByte(raw, '.'); i >= 0 {
		return raw[:i], raw[i+1:]
	}

	return "", raw
}

// clampPageSize 钳制每页条数到 [1, maxSize]：该参数常为远端可控输入，
// 上限防止无界查询。
func clampPageSize(size, maxSize int) int {
	if size <= 0 {
		size = defaultPageSize
	}

	return min(size, maxSize)
}

func normalizeOrder(order string) string {
	if strings.EqualFold(order, orderDesc) {
		return orderDesc
	}

	return orderAsc
}

// isSafeColumn 列名文法校验：点分 1~2 段（column 或 table.column），
// 每段必须是合法标识符 [A-Za-z_][A-Za-z0-9_]*——纯数字（位置排序 "ORDER BY 1"）、
// 空段（".id"、"id."、"a..b"）、超过两段一律拒绝。
// 这是防注入与防位置排序的底线；列名本身经 clause.Column 由方言引擎加引号。
func isSafeColumn(column string) bool {
	if column == "" {
		return false
	}
	segments := strings.Split(column, ".")
	if len(segments) > maxColumnSegments {
		return false
	}
	for _, seg := range segments {
		if !isIdentifier(seg) {
			return false
		}
	}

	return true
}

// isIdentifier 校验单段标识符：首字符字母或下划线，其余为字母、数字或下划线。
func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z':
		case '0' <= r && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}

	return true
}

// clampPage 把页码钳制到 [1, totalPage]；无数据（totalPage==0）时返回 0。
func clampPage(page, totalPage int) int {
	if totalPage == 0 {
		return 0
	}

	return min(max(page, 1), totalPage)
}

// calcTotalPage 计算总页数（向上取整）。用除法加余数实现，任意 totalCount
// （含 math.MaxInt64）都不会发生加法溢出；再做两道上界保护，
// 保证 (page-1)*pageSize 的 offset 乘法与 int 转换在任何平台都安全。
func calcTotalPage(totalCount int64, pageSize int) int {
	if totalCount <= 0 || pageSize <= 0 {
		return 0
	}

	size := int64(pageSize)
	pages := totalCount / size
	if totalCount%size != 0 {
		pages++
	}

	return int(clampPages(pages, size, math.MaxInt))
}

// clampPages 把总页数钳制到平台安全上界：
// ① 末页 offset 需满足 (page-1)*pageSize <= maxInt；② 页数本身不得溢出 int。
// maxInt 作为参数传入（生产传 math.MaxInt），使 32 位边界能在 64 位机上被测试执行。
func clampPages(pages, size, maxInt int64) int64 {
	// 独立的内部函数，不依赖外部保证：size 非正时直接返回 0 而非在除法处 panic。
	if size <= 0 {
		return 0
	}
	// 用 pages-1 与商比较，避免 maxOffsetPages+1 在 size==1 时溢出。
	if maxOffsetPages := maxInt / size; pages-1 > maxOffsetPages {
		pages = maxOffsetPages + 1
	}
	if pages > maxInt {
		pages = maxInt
	}

	return pages
}
