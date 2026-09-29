# 变更记录

本文档记录 `github.com/gtkit/ormx` 的对外可见变更。

格式参考 Keep a Changelog，版本号遵循 Semantic Versioning 2.0.0。本模块只维护 `v1` 主线，不发布新的 major：确需破坏性变更时以 MINOR 版本发布，且只允许 fail-closed 形态（旧的危险行为改为显式报错并在错误信息中指引出路），对应条目顶部以 **⚠ 破坏性变更** 标注并附迁移说明。

## [Unreleased]

## [v1.5.3] - 2026-09-29

### Fixed

- `zlogger`：修复 SQL 日志的 `source` 字段恒指向 zlogger 自身文件、无法定位业务调用位置的问题；经 `WithLogger` 注入的 zap logger 不再输出指向 zlogger 内部的 `caller` 字段，调用位置统一由 `source` 给出。

## [v1.5.2] - 2026-09-28

### Changed

- 文档：分页深分页开销、受限投影排序键、`Config` 配置文件映射与 `OpenWithDB` 失败后的约束改为正向使用说明。

## [v1.5.1] - 2026-09-28

### Fixed

- README 包清单补充 `paginator` 子包，与实际提供的三个包一致。
- CHANGELOG 版本策略说明与实际执行口径对齐：仅维护 `v1` 主线，破坏性变更以 fail-closed 形态按 MINOR 发布并附迁移说明。

## [v1.5.0] - 2026-07-30

**⚠ 破坏性变更**：单列链式 `.Distinct(col)` 分页不再自动统计总数，未提供 `WithTotal` 时返回 `ErrTotalRequired`。v1.4.x 下这类查询会以 `count(DISTINCT col)` 自动统计，但该值排除 NULL、与数据投影保留一个 NULL 的行数不一致，会让总数与页数静默偏小。行为改为显式报错（fail-closed），不再返回可能错误的分页元信息。

迁移：对单列 DISTINCT 分页，自行统计去重行数后经 `WithTotal` 传入，例如 `SELECT COUNT(*) FROM (SELECT DISTINCT col FROM t WHERE …) x`（子查询计数保留 NULL 行，与分页数据一致）；GROUP BY 查询不受影响，仍可自动统计。

### Changed

- `paginator`：所有 DISTINCT 查询现在均要求 `WithTotal`（见上方破坏性变更说明），修复单列可空字段中 `COUNT(DISTINCT col)` 排除 NULL、而数据投影保留一个 NULL，导致总数与页数静默偏小的问题。

### Fixed

- `paginator`：修复 `Distinct` 与 `Group` 并存时错误混用分组数量作为去重结果总数的问题。
- `paginator`：嵌套 scope 对 ORDER BY、LIMIT、OFFSET、Select、Distinct、Group 或 Raw SQL 的危险改写改为在 SQL 构建前返回 `ErrDeferredPaginationClause`，不再先执行被取消 LIMIT 的无界查询；安全的普通追加排序仅保留在数据查询中，并从 Count 移除。
- `paginator`：拒绝 `db.Raw(...)` 句柄，修复 GORM 因 `Statement.SQL` 已存在而忽略分页 Clause、却静默返回未分页数据的问题；原始 Select 会跳过前置注释和优化器 hint 后再识别 `DISTINCT`/`DISTINCTROW`。
- `paginator`：完整拒绝缺少 Config、Dialector、ConnPool、Statement、Statement.DB 或 Context 的部分初始化 GORM 句柄，统一返回 `ErrNilDB`，避免进入 GORM `Session` 或 schema 解析后 panic；文档明确 Scope 调用次数和 `Items` 仅在成功返回时恒非 nil。

## [v1.4.1] - 2026-07-30

### Fixed

- `paginator`：修复两处会**静默返回错误分页结果**的缺陷（均在真实 MySQL 上复现后修复）。
  - 嵌套 scope 可用 `clause.OrderBy{Expression: …}` 顶替整个排序而不被察觉：`clause.OrderBy.Build` 一旦发现 `Expression` 非 nil 就完全忽略 `Columns`，而 `MergeClause` 会把本包的 `Columns` 原样复制进对方、只保留对方的 `Expression`——原先只比对 `Columns` 前缀的校验因此被骗过（实测生成 `ORDER BY RAND()`，OFFSET 分页重复且遗漏）。校验改为同时要求 `Expression` 为 nil；受限投影下进一步要求排序列完全相等（此时本包不追加主键次级排序，行序完全依赖调用方给的那一列，追加投影外的列还会被数据库拒绝）。
  - Scopes 里注入的 `Distinct`/`Group` 不进入统计语义判定：投影在构建前不可见，导致数据查询去重/分组而统计查询不去重（实测一对多 JOIN + scope 内 `Distinct()`，3 个实体的查询返回 `TotalCount=6` 并产生空尾页）。投影判定改为在语句执行后再做一次并与执行前比对，不一致即返回 `ErrDeferredPaginationClause`。原先文档声称这类情况「一律响亮失败」，与事实不符，已更正。
- `paginator`：`ErrDeferredPaginationClause` 的语义扩展为「延迟机制改变了本包据以工作的语句状态」，涵盖分页/排序子句被覆盖与投影被改两类原因（错误文本同步更新）。命名沿用旧符号以保持 v1.4.x 内的 API 兼容。
- `paginator`：原始字符串去重的识别按 Unicode 空白判定关键字边界（此前只认空格与 Tab，`Select("DISTINCT\nname")` 会漏检），并补上 `clause.NamedExpr` 形式的 SELECT 子句。
- `paginator`：手工构造的零值 `&gorm.DB{}`（`Statement` 为 nil）此前会在 GORM 内部 panic，改为在入口返回 `ErrNilDB`。
- `paginator`：修正文档口径——`WithTotal` 的 GoDoc 不再声称 DISTINCT/GROUP BY 一律必需（单列链式 Distinct 与 GROUP BY 可自动统计）；`projectionOf` 注释不再声称多列链式 Distinct 的 Count 可靠；补充说明 `Table` 表达式含 JOIN/多表时自动追加的主键次级排序也是裸列名、同名主键会触发 MySQL Error 1052，以及 scope 里改 `Select` 不在比对范围内。

## [v1.4.0] - 2026-07-30

### Added

- 新增子包 `paginator`：基于 GORM 的通用分页查询执行器。泛型 `Paginate[T](db, Params, ...Option) (Page[T], error)`。分页三子句（ORDER/LIMIT/OFFSET）由本包全权负责——入参残留子句清除，且这三个子句以**追加在最后的 scope** 形式下发，写在 `Scopes` 里的排序与分页（GORM 官方文档的典型用法）会被本包覆盖而非反过来截断统计、破坏排序；scope 内部再注册 scope（组合式 helper）会被 GORM 排到本包之后，此时在语句执行后按事实校验三子句并返回 `ErrDeferredPaginationClause`（不发额外 SQL、不重跑调用方 scope；数据库同时报错时根因一并包在同一错误里），而不是静默产出总数为 0 的空页；入参句柄零污染（进入时即私有化 Statement，可安全复用，并发把同一句柄传入亦安全，`-race` 覆盖）；排序经 `clause.OrderBy` 结构化下发、列名由方言引擎加引号（保留字列如 `order` 可安全排序，不拼接原始 SQL），排序列与主键列在表名可判定时自动加表名限定（消除 Join 列歧义；`Table("t AS a")`/`Table("t a")` 用别名、`Table("t")`/`Table("db.t")` 用表名，`Table` 表达式含 JOIN/多表时取不准则一律不限定，避免生成 FROM 里不存在的限定名；别名/计算列不误加前缀）；默认按模型主键排序并自动追加全部主键列（含复合主键）作次级排序、方向跟随主排序、同列不同写法去重，在数据集不变且排序键组合唯一时翻页不重不漏；受限投影（链式 `.Distinct`/`.Group`、结构化 `clause.Select{Distinct}`/`clause.GroupBy`、原始字符串 `Select("DISTINCT …")`）必须显式提供排序列（否则返回 `ErrSortRequired` 且不发出任何 SQL），该列不做表名限定、不追加主键次级排序；统计语义按写法区分并以真实 MySQL 8 实测为准——**单列**链式 `.Distinct(col)` 生成 `count(DISTINCT col)`、GROUP BY 取统计查询返回行数（即分组数量，代价为 O(分组数) 行传输，高基数分组请用 `WithTotal`），两者可自动统计；**多列**链式 Distinct、`clause.Select{Distinct}`、原始字符串 DISTINCT/DISTINCTROW（含 `clause.Select{Expression: clause.Expr{SQL: "DISTINCT …"}}` 这种结构化外壳套原始串的形式）一律退化为 `count(*)` 返回总行数而非去重数，必须经 `WithTotal` 提供总数，否则返回 `ErrTotalRequired`（判定比 GORM 的条件更严，只会多要求 `WithTotal`，不会反向放行错误总数）；写在 `Scopes` 里的 `Distinct`/`Group` 构建前不可见（v1.4.1 起由执行后的投影比对拦下，见该版本条目）；PageSize 钳制上限（默认 100，`WithMaxPageSize` 调整），任意 Option 值（含 `math.MaxInt`）与任意总数（含 `math.MaxInt64`）下均不 panic、元信息不溢出（总页数用除法实现并做 offset/int 双重上界保护，不按 pageSize 预分配切片）；入参句柄的 `db.Error` 在入口包装返回、模型不可解析时透传 `parse model` 根因；未配置映射时 `Params.Sort` 作为远端输入只接受本模型字段（跨表引用与未知列回退默认排序）；`WithSortMapping` 外部键→受信任列显式映射（nil 报错、构造期防御性复制）、`WithDefaultSort`、`WithTotal`（三者由开发者配置，视为受信任列）；列名文法校验拒绝纯数字位置排序与空段；Items 恒非 nil。Count 语义限制（Select/Joins）、非一致性快照、深分页与排序稳定性前提均在文档如实声明；并发语义、`WithTotal` 准确性、方言验证范围（仅 MySQL 8）等边界均在文档如实声明；真实 MySQL 集成测试（DISTINCT/多列 DISTINCT 总数/GROUP BY/结构化 Distinct/Scopes 注入分页与 GROUP BY/嵌套 scope 覆盖/保留字/Join 同名列/JOIN 表达式/表别名三形态/复合主键/Preload/Count 隔离/翻页不重不漏）为发布前置条件，`make tag` 强制执行且不可经环境变量跳过

### Changed

- 发版脚本 `make tag` 补齐 `go mod tidy -diff`、gosec 与真实 MySQL 集成测试（发布前置条件，运行开关硬编码且 `ORM_REQUIRE_INTEGRATION=1` 让缺条件从 skip 变为 fail，防止静默跳过后照常打 tag）；`make tool` 改为只读检查（gofumpt 不一致即失败），写文件的格式化移到新增的 `make fmt`；新增 `make test-integration`

## [v1.3.0] - 2026-07-24

### Added

- 新增 `WithCharset` Option，一行显式设置连接字符集：连接后执行 `SET NAMES <charset>`，配 `WithCollation` 时执行 `SET NAMES <charset> COLLATE <collation>`；不再需要自建 `*sql.DB` 绕行。标识符仅允许字母/数字/下划线；仅支持单一字符集，回退列表（`utf8mb4,utf8`）返回新哨兵错误 `ErrDSNUnsupported`（可用 `errors.Is` 判定），回退列表需要时经 `WithDSN` 的 `charset=` 参数设置
- 新增 `WithDSN` Option，直接以完整 MySQL DSN 初始化连接配置（整体替换 MySQL 子配置，DSN 未写的参数按驱动默认）：本库未单独建模的驱动参数（`multiStatements`、`maxAllowedPacket`、charset 回退列表等）原样透传给驱动、不丢失；后续 Option 仍可覆盖单个字段（含 `WithHost`/`WithPort`），TLS 名或连接地址被覆盖时派生 TLS 状态（明文回退、证书 ServerName）会按新值重新标准化；驱动对已移除参数（如 `strict`）的 panic 被转换为 `ErrDSNUnsupported` 错误
- 新增 `Client.Transaction(ctx, fn, txOpts...)` 事务便捷入口，等价 `WithTx(ctx, nil, fn, txOpts...)`，最常见调用不再写 `nil` 占位参数
- 新增 `WithZapLogger` Option，直传 `*zap.Logger` 一步接入 SQL 日志，等价 `WithZlogger(zlogger.WithLogger(zlog), opts...)`

## [v1.2.0] - 2026-07-23

### Added

- 导出哨兵错误 `ErrAddressRequired`（原未导出），调用方可用 `errors.Is` 判定 Open 因缺少连接地址而失败
- 导出哨兵错误 `ErrNilSQLDB`、`ErrNilTxFunc`（原未导出），调用方可用 `errors.Is` 判定向 `OpenWithDB` 传入 nil `*sql.DB`、或向 `WithTx` 传入 nil 事务函数
- 导出哨兵错误 `ErrSystemVariableNameRequired`，可用 `errors.Is` 判定系统变量名为空或纯空白

### Security

- `RedactedDSN`（及依赖它的 `Config.String()` / `GoString()`）现在除密码外，还会脱敏连接系统变量（SystemVariables）值与连接属性（ConnectionAttributes），避免 `session_secret` 等敏感绑定值随日志泄露
- `MySQLConfig` 新增脱敏的 `String()` / `GoString()`：直接以 `%v` / `%+v` / `%#v` 打印 `Config.MySQL` 子结构时，密码、参数值与连接属性同样被脱敏，不再泄露明文
- `Client.Config()` 返回的配置快照会把密码、连接参数值与连接属性统一脱敏为占位符，不再返回明文凭据或敏感绑定值；脱敏逻辑统一由 `MySQLConfig` 内部方法提供，各脱敏路径（`String`/`GoString`、`RedactedDSN`、`Client.Config`）共用同一敏感字段清单。打印路径之外的脱敏保证仅覆盖 `fmt`/`Stringer`，请勿将原始 `Config`/`MySQLConfig` 直接用于结构化日志（如 `slog.Any`）
- `WithSystemVariable`/`WithSystemVariables` 的值作为原始 SQL 拼入 `SET` 语句，仅接受可信静态配置——不可信输入（HTTP 参数、用户配置等）存在会话级 SQL 注入风险，GoDoc/README 已明确该边界；空的系统变量名现在会在构建连接配置（`Open`/`RedactedDSN`）时返回错误
- `WithGormLogger(nil)` 或直接构造 nil Logger 不再绕过安全默认：`gormConfig()` 会把 nil 兜底为 `gormlogger.Discard`，避免 GORM 恢复自己的默认日志器（stdout、含参数）
- `zlogger` 参数化查询默认改为开启（原默认关闭）：注入真实 zap logger 时默认不再把绑定参数写入日志；调试需要真实参数值时用 `WithParameterizedQueries(false)` 显式关闭

### Fixed

- `Config.Clone()`（及 `Client.Config()`）现在深拷贝连接池指针与 `Dialect.DefaultDatetimePrecision`，修复改动副本会污染原配置的问题
- `WithLocation(nil)` 不再覆盖默认时区；且 `driverConfig` 仅在 `Loc != nil` 时赋值，直接构造 `Config`/DTO 映射遗漏时也不会把驱动默认 `time.UTC` 改成 nil，避免启用 `ParseTime` 后因 nil `time.Location` 触发 panic
- 修复事务重试与启动 Ping 在极大 `maxRetries` 时 `maxRetries+1` 整数溢出、导致循环零次并静默跳过操作却返回 nil 的问题
- `Client.Close` 现在先关闭 GORM 预编译语句缓存（启用 `WithPrepareStmt` 时），修复 `OpenWithDB` 场景下缓存不被释放、服务端预编译语句泄漏的问题
- `WithNetwork("unix")` 未配 `Addr` 时不再把默认 `Host`/`Port` 拼成非法地址 `unix(127.0.0.1:3306)`，改为返回 `ErrAddressRequired`，强制用 `WithAddress` 指定 socket 路径
- `WithSlowThreshold` 忽略负值（保留默认 200ms），修复传入负阈值会把所有查询误记为慢查询的问题

### ⚠ 破坏性变更

- 移除集群读写分离能力（`Cluster` 及其全部方法、`OpenCluster` / `NewCluster` / `NewClusterWithOptions`、`ClusterOption`、`Node`、`ClusterHealthReport`、`ErrNoReadableNode` / `ErrPrimaryUnavailable` / `ErrClusterClosed`），以及写后读一致性窗口（`ContextWithWriteFlag` / `ContextWithWriteWindow` / `ContextClearWriteFlag` / `HasWriteFlag`）。需要读写分离的下游请改用 `gorm.io/plugin/dbresolver`，并复用 `Client.HealthCheck` 做探活。
- 健康模型收敛为单机：移除 `NodeRole` / `NodeState` 类型及 `RoleStandalone` / `HealthStatusDegraded` 等枚举值；`HealthReport` 去掉 `Role`、`State` 字段（`State` 可由 `Status` 推导）；`HealthProbeFunc` 去掉 `role` 参数，签名变为 `func(ctx context.Context, client *Client) error`；连接池指标不再附带 `role` 标签。
- `PoolConfig` 字段由值类型改为指针（`*int` / `*time.Duration`）：`nil` 表示不设置、保持 database/sql 默认，非 `nil`（含 0）表示显式应用。修复直接结构体赋值或 JSON/YAML 映射连接池参数时因内部标记未置位而静默失效的问题；`WithMaxOpenConns` 等 Option 用法不变。
- 重命名 `WithDSNParam` / `WithDSNParams` 为 `WithSystemVariable` / `WithSystemVariables`，并将 `MySQLConfig.Params` 字段重命名为 `SystemVariables`（JSON/YAML 标签 `params` → `system_variables`）：其值写入 `mysql.Config.Params`，语义是连接后执行的系统变量 `SET key = value`（非 DSN 内置参数）。旧名称易误用于 charset 等内置参数，故正名；loc/parseTime/timeout 等请用对应专用 Option（charset 无专用 Option，如需自行用 mysql.Charset 构建 *sql.DB 再经 OpenWithDB 接入）。
- 移除无效的 `WithDriverName` 与 `MySQLDialectConfig.DriverName`：本库始终以现成 `*sql.DB`（`Conn`）初始化 GORM，驱动仅在 `Conn == nil` 时才使用 `DriverName`，故该配置从不生效，删除不影响任何运行行为。
- **默认日志改为静默**：未注入 Logger 时默认使用 `gormlogger.Discard`（原为 GORM 默认 Warn、输出到 stdout 且不隐藏参数）。库不再隐式输出 SQL 或泄露绑定值，日志需用 `WithZlogger`/`WithGormLogger` 显式开启。
- 移除 `Client.Metrics()` 与 `MetricSample`：与 `StatsSnapshot()` 信息重复且无法区分 gauge/counter，改由业务监控层基于 `StatsSnapshot()` 自行对接（正确选择 gauge/counter 语义）。
- 移除 `MySQLDialectConfig` 中仅用于旧版 MySQL/MariaDB 兼容、且无 Option/无测试的 `DontSupport*` 字段（6 个）：本库目标为 MySQL 8，GORM 会在初始化时按版本自动推导这些标志。
- `zlogger.GormLogger` 类型改为非导出：其字段全私有、`New` 返回 `gormlogger.Interface`，导出具体类型无实际用途。
- `OpenWithDB` 不再对借用的外部连接池强加包内默认（50/10/30m/10m），仅应用调用方显式传入的池 Option，未覆盖项保持外部 `*sql.DB` 现有设置。
- 移除 `Client.Stats()`：与 `SQLDB().Stats()` 完全等价，请改用后者或 `StatsSnapshot()`。
- `Config.DriverConfig()` 不再导出（原会暴露含明文密码的驱动配置）；内部构建改用私有路径。
- `zlogger` 参数化查询默认改为开启（见 Security 一节，属默认行为变更）。

### Removed

- 删除集群相关源码与测试，主包回归单机连接、事务与健康/可观测能力；`Client.HealthCheck` / `StatsSnapshot` / `Name` 与 `WithHealthProbe` 仍提供，但相关类型、字段与签名有调整（见上文「破坏性变更」）

### Changed

- 发版脚本 `make tag` 门禁补齐 golangci-lint、覆盖率 ≥ 80%、benchmark 与 govulncheck；README 发版说明移除 `make tag BUMP=major`（本项目只维护 v1，major 被脚本拒绝，破坏性变更按 MINOR 发布）
- 打开连接与事务的错误增加 `ormx:` 阶段前缀（创建连接器、启动 Ping、初始化 GORM、事务 begin/commit/回滚、`Client.Close` 等），通过 `%w` 保留 `errors.Is`/`errors.As` 判定能力，仅提升可观测性；事务函数返回错误且回滚成功时直接返回原错误（不再无谓 join）

## [v1.1.3] - 2026-07-21

### Added

- 新增 `WithZlogger(opts ...zlogger.Option)`，一步注入 zap SQL 日志器，等价 `WithGormLogger(zlogger.New(opts...))`，无需显式调用 `zlogger.New`

### Fixed

- 修复 `zlogger` 在配置 `TraceIDExtractor` 时，错误、慢查询与全量查询日志中 `trace_id` 字段重复出现的问题

## [v1.1.2] - 2026-07-21

### Changed

- 明确 SQL 日志默认行为，以及通过 `gormlogger.Discard`、Warn、Info 和 `zlogger` 控制输出的配置方式
- `zlogger` 推荐配置显式启用参数化查询，并补充绑定参数可能包含敏感信息的风险说明

### Fixed

- 修复 `zlogger` 在 Warn 级别遇到正常快查询时仍生成 SQL 字符串的问题，无日志快路径不再调用 SQL 回调
- 修正包内 `Version` 与已发布的 `v1.1.1` 标签不一致的问题

## [v1.1.1] - 2026-07-06

### Added

- 新增 MIT LICENSE，pkg.go.dev 可正常展示模块文档
- 新增可判断的集群路由错误 `ErrNoReadableNode`、`ErrPrimaryUnavailable`、`ErrClusterClosed`，可用 `errors.Is` 区分读写路由的失败类型
- 新增根包 package 文档（pkg.go.dev 包摘要与用法概述）

### Changed

- 导出 API 的 GoDoc 统一为简体中文（语义不变）
- 文档：`DrainReplica` 补充与健康巡检自动恢复的交互说明——draining 副本若探活失败被置为 down，恢复后会被自动拉回读池，长期摘除需暂停健康循环
- 文档：明确 `Config` 从配置文件映射时须以 `DefaultConfig()`/`NewConfig()` 为基底，零值直接反序列化会使连接池配置被静默忽略

## [v1.1.0] - 2026-06-16

### ⚠ 破坏性变更

- 移除 `github.com/gtkit/ormx/jetorm` 子包，已分离为独立模块 [`github.com/gtkit/jetx`](https://github.com/gtkit/jetx)。仅使用 go-jet 的下游不再被动引入 GORM 依赖；ormx 自身也不再传递 `go-jet/jet`、`google/uuid` 等依赖。
- 迁移方式：将 import `github.com/gtkit/ormx/jetorm` 改为 `github.com/gtkit/jetx`，包名前缀 `jetorm.` 改为 `jetx.`，行为与超时治理模型完全不变。

### Removed

- 移除 `ormx/jetorm` 子包及其对 `go-jet/jet`、`google/uuid` 的依赖

## [v1.0.4] - 2026-06-12

### 修复

- 修复发版脚本的标签创建与推送顺序，避免发布 tag 落后于当前提交。

## [v1.0.3] - 2026-06-12

### 修复

- 修复发版脚本推送远程配置后，当前提交未被最新 tag 覆盖的问题。

## [v1.0.2] - 2026-06-12

### 修复

- 修复根包 `Client.WithTx`/`WithReadTx` 传入 nil context 且触发死锁重试时 panic 的问题：现在入口统一标准化，nil ctx 在全部路径（含重试退避等待、`TxRetryObserver` 回调）等价于 `context.Background()`
- 修复事务死锁重试与启动 Ping 重试的退避计算在重试次数极大（约 ≥41 次）时整数溢出导致 panic 或零退避的问题：溢出时按退避上限处理
- 修复 `jetorm` 的 `Client.WithTx` 在事务函数出错且回滚也失败时丢弃回滚错误的问题：现在通过 `errors.Join` 将回滚错误与原始错误一并返回（与根包 `ormx.Client.WithTx` 行为一致）；事务已被终止导致的 `sql.ErrTxDone` 不计为回滚失败。对原始错误的 `errors.Is`/`errors.As` 判断不受影响

### 变更

- 补全全部导出 API 的 GoDoc 注释，并为根包、`jetorm`、`zlogger` 的核心配置 API 新增 Example 示例（pkg.go.dev 可见）

## [v1.0.1] - 2026-06-11

### 变更

- 重写 README：补充根包、zlogger、jetorm 的完整使用说明与全部选项函数表格，新增 jetorm 与 GORM 的使用场景分工指引

## [v1.0.0] - 2026-06-10

首个版本。代码基线：`github.com/gtkit/orm/v2` v2.3.1（含 commit 1c06e07 的 timer 修复）与 `github.com/gtkit/orm/jetorm`（v1.3.2）。

### 新增

- 根包 `ormx`：自 `orm/v2` 全量平移（连接管理、集群读写分离、健康探活、事务死锁重试、写后读一致性窗口、启动探活重试、事务重试观测、指标采样），包名 `orm` → `ormx`
- `ormx/zlogger`：自 `orm/v2/zlogger` 平移（单份，不再与 v1 双份并存）
- `ormx/jetorm`：自 `orm/jetorm` 收编，新增：
  - `WithTxTimeout(...)`：显式事务总时长上限
  - `WithTxRetry(maxRetries, baseWait, maxWait)`：死锁（1213）/锁等待超时（1205）自动重试，默认关闭
  - `WithDSNParam(...)`、`WithDialTimeout(...)`、`WithReadTimeout(...)`、`WithWriteTimeout(...)`、`WithLoc(...)`
- `ormx/internal/dsn`：根包与 jetorm 共享的驱动配置、连接池默认值、退避与死锁判定（消除旧仓库的三份重复实现）

### 变更（相对旧 jetorm，BREAKING）

- `QueryTimeout` 不再隐式限制事务生命周期，仅作用于单条语句；事务总时长由 `WithTxTimeout` 控制

### 依赖

- gorm v1.31.1、go-sql-driver/mysql v1.10.0、go-jet v2.15.0、zap v1.28.0
