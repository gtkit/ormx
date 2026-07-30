# ormx

基于 GORM 的 MySQL 数据访问封装，包含两个包：

| 包 | 用途 |
|----|------|
| `github.com/gtkit/ormx` | 基于 GORM 的客户端——连接与连接池配置、事务死锁自动重试、单机健康探活与可观测 |
| `github.com/gtkit/ormx/zlogger` | GORM 的 zap 日志适配——慢查询阈值、trace id 提取、SQL 参数脱敏 |


## 安装

```bash
go get github.com/gtkit/ormx
```

---

## 根包 ormx（GORM 客户端）

### 快速开始

```go
import "github.com/gtkit/ormx"

client, err := ormx.Open(ctx,
    ormx.WithHost("127.0.0.1"),
    ormx.WithPort("3306"),
    ormx.WithDatabase("app"),
    ormx.WithUser("root"),
    ormx.WithPassword("secret"),
)
if err != nil {
    return err
}
defer client.Close()

db := client.DB() // *gorm.DB，直接走 GORM API
```

配置里已有现成 DSN 时，可用 `ormx.WithDSN("user:pass@tcp(host:3306)/db?parseTime=true")` 一行替代上面的连接 Option（语义与限制见下文选项表）。

### 打开方式

| 入口 | 说明 |
|------|------|
| `ormx.Open(ctx, opts...)` | 按 Option 构建配置并连接，最常用 |
| `ormx.MustOpen(ctx, opts...)` | 同上，失败时 panic，适合启动期 wiring |
| `ormx.OpenWithDB(ctx, sqlDB, opts...)` | 复用已有 `*sql.DB`（只应用显式传入的池 Option）；打开成功后 `Close()` 不关闭外部 DB，但**初始化失败时 GORM 可能关闭该 DB，失败后勿再复用**（详见 GoDoc） |
| `ormx.NewConfig(opts...)` / `cfg.With(opts...)` / `cfg.Open(ctx)` | 先构建 `Config` 值再打开，适合多实例复用基础配置（`Config` 是运行期配置，配置文件请由业务侧 DTO 转成 Option，详见 `Config` 的 GoDoc） |

`Config` 通过 `With` / `Clone` 返回隔离副本（仅复制包内可变字段：`SystemVariables` map、连接池与方言指针），不修改原值；注入的 `GORM.Logger`、`HealthProbe`、`TxRetryObserver`、`NamingStrategy.NameReplacer` 与 `Loc` 仍为共享引用。普通赋值（`cfg2 := cfg`）是浅拷贝，需独立副本时用 `Clone`/`With`。`Config.String()`（及 `MySQLConfig.String()`）与 `%v` / `%+v` / `%#v` 输出会把密码、参数值与连接属性脱敏为 `******`，可放心打日志；`cfg.RedactedDSN()` 返回脱敏后的 DSN 字符串。注意：脱敏仅覆盖 `fmt`/`Stringer` 路径，**不要把原始 `Config`/`MySQLConfig` 直接传给结构化日志器（如 `slog.Any`）或用于序列化日志**——请改用 `String()` 或 `RedactedDSN()`。

```go
base := ormx.NewConfig(
    ormx.WithHost("db.internal"),
    ormx.WithUser("app"),
    ormx.WithPassword(os.Getenv("DB_PASSWORD")),
)
orders, err := base.With(ormx.WithDatabase("orders"), ormx.WithName("orders")).Open(ctx)
users, err  := base.With(ormx.WithDatabase("users"), ormx.WithName("users")).Open(ctx)
```

### 选项函数

#### 连接与 DSN

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithName(name)` | `"default"` | 实例名，用于 `Client.Name`、健康报告与事务重试事件；多实例时建议显式设置 |
| `WithDSN(dsn)` | — | 以完整 DSN（如 `user:pass@tcp(host:3306)/db?parseTime=true`）**整体替换** MySQL 连接子配置：DSN 未写的参数按驱动默认（parseTime=false、时区 UTC、无超时），不叠加本包默认；本库未单独建模的驱动参数（`multiStatements`、`maxAllowedPacket`、charset 回退列表等）原样透传给驱动，不丢失。建议放在其它连接 Option 之前，之后的 Option 仍可覆盖单个字段（含 `WithHost`/`WithPort`）。DSN 仅应来自可信静态配置，不得直接接收用户输入，也不要记录含凭据的原始 DSN（日志用 `RedactedDSN`）——透传的参数中可能包含影响安全边界的驱动开关 |
| `WithHost(host)` | `127.0.0.1` | 主机；设置后清空 Addr |
| `WithPort(port)` | `3306` | 端口；设置后清空 Addr |
| `WithAddress(addr)` | 空 | 完整地址（`host:port`），优先级高于 Host/Port |
| `WithNetwork(network)` | `tcp` | 网络类型；用 `unix` 时必须配 `WithAddress("/path/mysql.sock")` 指定 socket 路径，否则 Open 返回 `ErrAddressRequired` |
| `WithDatabase(name)` | 空 | 数据库名 |
| `WithUser(user)` | 空 | 用户名 |
| `WithPassword(password)` | 空 | 密码（日志输出自动脱敏） |
| `WithParseTime(enabled)` | `true` | 是否把 DATETIME 解析为 `time.Time` |
| `WithLocation(loc)` | `time.Local` | DSN 时区 |
| `WithTimeout(d)` | `10s` | 建连超时 |
| `WithReadTimeout(d)` | `30s` | I/O 读超时 |
| `WithWriteTimeout(d)` | `30s` | I/O 写超时 |
| `WithTLSConfig(name)` | 空 | TLS 配置名：支持驱动内置值 `true` / `false` / `skip-verify` / `preferred`（无需注册），或经 `mysql.RegisterTLSConfig` 注册的名称。生产环境推荐 `true` 或启用证书验证的自定义配置；`preferred` 可能回退明文连接、`skip-verify` 不验证服务端证书，仅适合受控环境 |
| `WithCharset(charset)` | 驱动默认（utf8mb4） | 显式指定连接字符集，连接后执行 `SET NAMES <charset>`；配 `WithCollation` 时执行 `SET NAMES <charset> COLLATE <collation>`。驱动默认已是 utf8mb4，非必选项；标识符仅允许字母/数字/下划线，仅支持单一字符集（回退列表返回 `ErrDSNUnsupported`，需要时经 `WithDSN` 的 `charset=` 参数设置） |
| `WithCollation(collation)` | 驱动默认 | 连接 collation |
| `WithConnectionAttributes(attrs)` | 空 | 连接属性（`performance_schema.session_connect_attrs`） |
| `WithSystemVariable(key, value)` | — | 追加连接系统变量，连接后执行 `SET key = value`；value 须是合法 SQL 表达式、且仅接受可信静态配置。**非** DSN 内置参数（`loc`→`WithLocation`、`parseTime`→`WithParseTime`、`charset`→`WithCharset` 等） |
| `WithSystemVariables(params)` | — | 批量追加连接系统变量，语义同上 |

#### 连接池

这四个选项透传到标准库 `sql.DB` 的对应方法（`Open` 用下列默认值初始化；`OpenWithDB` 只应用显式传入的项，其余保持外部 `*sql.DB` 原样）：

| Option | 默认值 | 透传到 | 取值语义 |
|--------|--------|--------|---------|
| `WithMaxOpenConns(n)` | `50` | `sql.DB.SetMaxOpenConns` | `n ≤ 0` 表示不限制打开连接数 |
| `WithMaxIdleConns(n)` | `10` | `sql.DB.SetMaxIdleConns` | `n ≤ 0` 表示不保留空闲连接 |
| `WithConnMaxLifetime(d)` | `30m` | `sql.DB.SetConnMaxLifetime` | `d ≤ 0` 表示连接不过期 |
| `WithConnMaxIdleTime(d)` | `10m` | `sql.DB.SetConnMaxIdleTime` | `d ≤ 0` 表示空闲连接不因闲置被关闭 |

```go
client, err := ormx.Open(ctx,
    ormx.WithHost("127.0.0.1"), ormx.WithDatabase("app"),
    ormx.WithUser("app"), ormx.WithPassword(os.Getenv("DB_PASSWORD")),
    ormx.WithMaxOpenConns(100),
    ormx.WithMaxIdleConns(20),
    ormx.WithConnMaxLifetime(time.Hour),
    ormx.WithConnMaxIdleTime(10*time.Minute),
)
```

> 注意：`database/sql` 会把 `MaxIdleConns` 自动限制到不超过 `MaxOpenConns`——调小 `MaxOpenConns` 时记得同步下调 `MaxIdleConns`，否则多出的空闲上限会被静默截断。

#### GORM 行为

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithGormLogger(log)` | `Discard`（静默） | 设置任意 `gormlogger.Interface` 实现；默认静默，不输出任何 SQL 日志 |
| `WithZlogger(opts...)` | 无参为 no-op（静默） | 一步注入 zap 日志器，等价 `WithGormLogger(zlogger.New(opts...))`；不传 Option 时用 zlogger 默认（no-op logger，静默丢弃），须至少 `zlogger.WithLogger(...)` 注入 zap。详见下文 zlogger 章节 |
| `WithZapLogger(zlog, opts...)` | — | 直传 `*zap.Logger` 一步接入，等价 `WithZlogger(zlogger.WithLogger(zlog), opts...)`（接 zap 的最短路径）；`nil` 回退 no-op，附加 `zlogger.Option` 在其后按序生效 |
| `WithPrepareStmt(enabled)` | `false` | 开启预编译语句缓存。默认关闭；适合长生命周期单例 Client，**不要频繁 Open/Close**（GORM 的 TTL 缓存清理 goroutine 不随 `Close` 退出，`Close` 仅释放已缓存语句） |
| `WithPrepareStmtCache(maxSize, ttl)` | GORM 默认 | 预编译语句缓存容量与 TTL，**仅在 `WithPrepareStmt(true)` 时生效**；不设置时沿用 GORM 的缓存默认 |
| `WithSkipDefaultTransaction(skip)` | `false` | 跳过 GORM 单条写操作的默认事务 |
| `WithNowFunc(fn)` | `time.Now` | GORM 时间函数（测试注入用） |
| `WithNamingStrategy(strategy)` | `IdentifierMaxLength: 64` | 整体替换命名策略 |
| `WithTablePrefix(prefix)` | 空 | 表名前缀 |
| `WithSingularTable(enabled)` | `false` | 使用单数表名 |
| `WithDefaultContextTimeout(d)` | `0`（不限制） | GORM 操作默认 context 超时 |
| `WithDefaultTransactionTimeout(d)` | `0`（不限制） | GORM 事务默认超时 |
| `WithDryRun(enabled)` | `false` | 只生成 SQL 不执行 |
| `WithQueryFields(enabled)` | `false` | SELECT 时展开全部字段名而非 `*` |
| `WithCreateBatchSize(n)` | `0` | 批量插入分批大小 |
| `WithTranslateError(enabled)` | `false` | 把驱动错误翻译为 GORM 错误（如 `ErrDuplicatedKey`） |

#### SQL 日志开关

**默认静默**：不传 `WithGormLogger` 时，本库默认注入 `gormlogger.Discard`，不向 stdout 输出任何 SQL、也不会泄露绑定参数。日志需显式开启，通过 `gormlogger.Interface` 控制，不需要额外布尔开关：

```go
// 默认即静默；如需显式关闭也可
ormx.WithGormLogger(gormlogger.Discard)

// 记录错误 SQL 与超过 200ms 的慢 SQL；
// 注意 gormlogger.Default 会把真实绑定参数插值进日志，仅用于受控开发环境
ormx.WithGormLogger(gormlogger.Default.LogMode(gormlogger.Warn))

// 记录全部 SQL；同样会输出真实绑定参数，仅用于受控开发环境
ormx.WithGormLogger(gormlogger.Default.LogMode(gormlogger.Info))
```

生产环境需要结构化日志时，建议使用下文的 `zlogger`，并开启参数化查询，避免 SQL 绑定参数进入日志。

#### 启动与健康

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithStartupPing(enabled)` | `true` | Open 时先 Ping 验证连通性 |
| `WithStartupPingRetry(maxRetries, baseWait, maxWait)` | `0, 1s, 5s` | 启动 Ping 失败后的重试次数与退避区间 |
| `WithHealthProbe(probe)` | 无 | 自定义健康探针，在 Ping 通过后追加执行（如跑一次轻量业务查询确认连接可用） |

#### 事务观测

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithTxRetryObserver(observer)` | 无 | 每次死锁重试前回调 `TxRetryEvent`（实例名、第几次、等待时长、错误），用于打点告警 |

#### MySQL Dialect（少用，对接非标准部署时才需要）

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithServerVersion(version)` | 自动探测 | 手工指定服务端版本，**仅在 `WithSkipInitializeWithVersion(true)` 时生效**；否则会被 `SELECT VERSION()` 结果覆盖。注意跳过版本探测后，GORM 不再据版本自动推导兼容标志 |
| `WithSkipInitializeWithVersion(skip)` | `false` | 跳过按版本初始化 |
| `WithDefaultStringSize(size)` | `0` | string 字段默认长度 |
| `WithDisableDatetimePrecision(disable)` | `false` | 禁用 datetime 精度（兼容 MySQL 5.6 以前） |
| `WithDisableWithReturning(disable)` | `false` | 禁用 RETURNING 子句 |

### Client 方法

| 方法 | 说明 |
|------|------|
| `DB() *gorm.DB` | 取 GORM 句柄 |
| `SQLDB() *sql.DB` | 取底层 `*sql.DB`（可交给 [jetx](https://github.com/gtkit/jetx) 等共享连接池） |
| `Config() Config` | 配置的脱敏快照（隔离副本，密码/参数值/连接属性已脱敏，不含明文凭据） |
| `Name() string` | 实例名（未设置时为 `default`） |
| `PingContext(ctx) error` | 连通性检查 |
| `StatsSnapshot() DBStatsSnapshot` | 连接池统计快照（额外含 `Utilization`），业务层据此自行对接监控；需原始 `sql.DBStats` 用 `SQLDB().Stats()` |
| `HealthCheck(ctx) HealthReport` | 健康检查（Ping + 自定义探针，默认 5s 超时） |
| `Transaction` / `WithTx` / `WithReadTx` | 事务，见下节 |
| `Close() error` | 关闭连接池（`OpenWithDB` 包装的实例不关闭外部 `*sql.DB`） |

### 事务（死锁自动重试）

```go
err := client.Transaction(ctx, func(tx *gorm.DB) error {
    if err := tx.Create(&order).Error; err != nil {
        return err
    }
    return tx.Model(&stock).Update("count", gorm.Expr("count - ?", 1)).Error
})
```

- `fn` 返回 nil 则提交，返回 error 则回滚；panic 时回滚后继续抛出。
- 遇到 MySQL 死锁（1213）或锁等待超时（1205）时自动按带抖动的指数退避重试，**默认最多 3 次**。重试意味着 `fn` 可能执行多次，事务内逻辑须幂等。
- 需要指定隔离级别/只读时用 `WithTx(ctx, &sql.TxOptions{...}, fn)`（`Transaction` 等价于 `WithTx(ctx, nil, fn)`）；`WithReadTx(ctx, fn)` 是 `ReadOnly: true` 的便捷形式。

每次调用可用 `TxOption` 覆盖重试行为：

| TxOption | 默认值 | 说明 |
|----------|--------|------|
| `WithMaxRetries(n)` | `3` | 最大重试次数，`0` 禁用重试 |
| `WithRetryBaseWait(d)` | `5ms` | 退避基础等待 |
| `WithRetryMaxWait(d)` | `50ms` | 单次退避上限 |

```go
err := client.Transaction(ctx, fn, ormx.WithMaxRetries(5), ormx.WithRetryMaxWait(200*time.Millisecond))
```

### 多库多实例

配置按实例隔离（没有全局状态），每次 `Open` 返回独立的 `*Client`，各自持有独立连接池。连接多个库就是创建多个实例，按依赖注入交给各业务模块：

```go
orderDB, err := ormx.Open(ctx, ormx.WithDatabase("orders"), ormx.WithName("orders") /* ... */)
userDB, err  := ormx.Open(ctx, ormx.WithDatabase("users"), ormx.WithName("users") /* ... */)

orderRepo := repo.NewOrderRepo(orderDB.DB())
userRepo  := repo.NewUserRepo(userDB.DB())
```

### 健康检查与连接池统计

单机 Client 提供健康检查与连接池统计快照，指标如何暴露（gauge / counter 语义）由业务监控层决定：

```go
report := client.HealthCheck(ctx)
if !report.Healthy() {
    log.Printf("db down: %v", report.Error)
}

s := client.StatsSnapshot()
// gauge 类（当前值）：OpenConnections / InUse / Idle / Utilization ...
openConns.Set(float64(s.OpenConnections))
// counter 类（累计值，用 Counter 而非 Gauge）：WaitCount / MaxIdleClosed / MaxLifetimeClosed ...
waitCountTotal.Add(float64(s.WaitCount))
```

`WithHealthProbe` 可在 Ping 之外追加业务探针，例如执行一次轻量查询确认连接可用：

```go
ormx.WithHealthProbe(func(ctx context.Context, c *ormx.Client) error {
    var one int
    return c.DB().WithContext(ctx).Raw("SELECT 1").Scan(&one).Error
})
```

### 错误处理

以下导出哨兵错误可用 `errors.Is` 判定：

| 错误 | 触发场景 |
|------|---------|
| `ormx.ErrAddressRequired` | 既未提供 `Addr`、又缺 `Host`/`Port`，或 `unix` 网络未用 `WithAddress` 指定 socket 路径 |
| `ormx.ErrNilSQLDB` | 向 `OpenWithDB` 传入 nil `*sql.DB` |
| `ormx.ErrNilTxFunc` | 向 `Transaction`/`WithTx` 传入 nil 事务函数 |
| `ormx.ErrSystemVariableNameRequired` | 系统变量名为空或纯空白 |
| `ormx.ErrDSNUnsupported` | DSN 级设置无法经当前 API 表达：`WithCharset` 传回退列表（`utf8mb4,utf8`）、用 `WithCharset("")` 清除来自 `WithDSN` 的 charset，或 DSN 使用驱动已移除的参数（如 `strict`） |

```go
if _, err := ormx.Open(ctx, /* ...缺少地址... */); errors.Is(err, ormx.ErrAddressRequired) {
    // 配置缺少连接地址
}
```

---

## zlogger（GORM 的 zap 日志适配）

`zlogger` 是实现 `gormlogger.Interface` 的 zap 日志器。用 `ormx.WithZapLogger` 直传 `*zap.Logger` 一步接入：

```go
import (
    "github.com/gtkit/ormx"
    "github.com/gtkit/ormx/zlogger"
    gormlogger "gorm.io/gorm/logger"
    "go.uber.org/zap"
)

zlog, _ := zap.NewProduction()

client, err := ormx.Open(ctx,
    // ...连接选项...
    ormx.WithZapLogger(zlog,
        zlogger.WithLogLevel(gormlogger.Warn),
        zlogger.WithSlowThreshold(300*time.Millisecond),
        zlogger.WithIgnoreRecordNotFoundError(true),
        zlogger.WithParameterizedQueries(true),
        zlogger.WithTraceIDExtractor(func(ctx context.Context) string {
            if id, ok := ctx.Value("X-Request-ID").(string); ok {
                return id
            }
            return ""
        }),
    ),
)
```

`WithZapLogger(zlog, opts...)` 等价于 `WithZlogger(zlogger.WithLogger(zlog), opts...)`，后者（`WithZlogger(opts...)` ≙ `WithGormLogger(zlogger.New(opts...))`）适合 Option 完全由外部组装的场景。需要注入自定义 `gormlogger.Interface` 实现（或已构造好的日志器）时，仍用 `WithGormLogger`。

多个数据库共享同一日志器时，建议给各实例的 zap logger 显式附加区分字段：

```go
ormx.WithName("orders"),
ormx.WithZapLogger(zlog.With(zap.String("database", "orders"))),
```

### 选项函数

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithLogger(log)` | nop（不输出） | 底层 `*zap.Logger`；**不设置则所有日志静默丢弃**，必须传入 |
| `WithLogLevel(level)` | `gormlogger.Warn` | 日志级别（Silent / Error / Warn / Info） |
| `WithSlowThreshold(d)` | `200ms` | 慢查询阈值；执行耗时超过即按 Warn 输出 `gorm slow query`，设为 `0` 关闭慢查询日志 |
| `WithIgnoreRecordNotFoundError(enabled)` | `false` | 忽略 `gorm.ErrRecordNotFound`，不作为错误日志输出 |
| `WithParameterizedQueries(enabled)` | `true`（安全默认） | 开启时日志中的 SQL 不带参数值（脱敏），只输出占位符语句；调试需要看真实参数值时显式传 `false` 关闭 |
| `WithTraceIDExtractor(fn)` | 无 | 从 context 提取 trace/request id，附加为 `trace_id` 字段，串联 SQL 日志与请求链路 |

### 输出行为

每条 SQL 日志包含字段：`source`（调用位置）、`elapsed`（耗时）、`sql`、`rows`（影响行数，-1 时省略）、`trace_id`（配置了 extractor 且能提取到时）。按以下优先级输出：

1. 执行出错（且未被 RecordNotFound 忽略）→ `Error` 级 `gorm query error`，附 `error` 字段
2. 耗时超过慢查询阈值 → `Warn` 级 `gorm slow query`，附 `slow_threshold` 字段
3. 日志级别为 Info → `Info` 级 `gorm query`（全量 SQL 日志，仅建议开发环境开启）

`LogMode` 遵循 GORM 约定返回调级别后的副本，可配合 `db.Session(&gorm.Session{Logger: ...})` 做局部调级。

> 安全提示：参数化查询默认开启（隐藏绑定参数）。仅在受控排障环境显式传 `WithParameterizedQueries(false)` 打开真实参数——它会把密码、Token 等敏感值写入日志。

---

## paginator（通用分页查询执行器）

`paginator` 基于 GORM 执行分页查询：Count 总数、钳制页码与页大小、按稳定排序执行 LIMIT/OFFSET，返回泛型结果。

```go
import "github.com/gtkit/ormx/paginator"

query := client.DB().WithContext(ctx).
    Model(&Topic{}).
    Where("category_id = ?", cid).
    Preload("Comments") // 预加载等查询装配直接在句柄上完成，本包不代理

page, err := paginator.Paginate[Topic](query,
    paginator.Params{Page: 2, PageSize: 20, Sort: "created", Order: "desc"},
    paginator.WithSortMapping(map[string]string{"created": "created_at"}), // 线上推荐
)
// page.Items（恒非 nil）/ CurrentPage / TotalPage / TotalCount
```

**核心契约：**

- **三子句全权负责**：入参句柄上残留的 ORDER BY / LIMIT / OFFSET 会被清除，分页与排序只由 `Params` 与 Option 决定；WHERE/Joins/Select 等其余条件原样保留。这三个子句以**追加在最后的 scope** 形式下发，所以写在 `Scopes` 里的排序与分页（GORM 官方文档正把分页列为 Scopes 的典型用法）会被本包覆盖，而**不会**反过来截断统计或破坏排序——scope 的 `Where` 等条件仍照常生效。
- **嵌套 scope 会被拦下**：GORM 的执行入口是「只要还有 scope 就再跑一轮」的多轮循环，**scope 内部再注册 scope**（组合式 helper）会排到下一轮、也就是本包之后，从而覆盖 LIMIT/OFFSET——统计被截断则总数静默为 0、翻页返回空页。`gorm.Statement.scopes` 未导出、包外无法排空，因此本包在语句**执行后按事实校验**三子句是否仍是自己设置的值，不一致即返回 `ErrDeferredPaginationClause`（不发额外 SQL、不重跑调用方 scope；若数据库也一并报错，根因会包在同一个错误里，用 `errors.Is` 都能判到）。嵌套 scope 只加条件时不受影响。
- **入参句柄零污染**：内部先把 Statement 私有化再工作，调用后原句柄可安全复用；多个 goroutine 并发把同一句柄传给 `Paginate` 也是安全的（`-race` 覆盖）。**但该句柄同时被用于其它查询（`Find`/`First`/`Count` 等）不安全**——这是 GORM 句柄语义所限，请各 goroutine 自行 `Session`/`WithContext` 派生。
- **结构化排序，不拼原始 SQL**：排序经 GORM 的 `clause.OrderBy` 下发，列名由方言引擎加引号——**保留字列（如 `order`）可安全排序**。列名文法校验（点分 1~2 段、每段合法标识符，拒绝纯数字位置排序与空段）作为第二道防线。
- **自动表名限定**：排序列与主键列在**表名可判定时**自动加表名前缀，消除 Join 场景的列歧义。可判定：纯 Model 查询、`Table("t")`、`Table("db.t")`、`Table("t AS a")`、`Table("t a")`、`Table("(SELECT …) AS a")`（带别名时用别名）。**不可判定**：`Table` 表达式含 JOIN／多表（GORM 提取不出别名，回退基表名会生成 FROM 里不存在的限定名 → Error 1054），此时一律不限定，Join 列歧义需调用方自行限定排序列。别名与计算列（不属模型字段）也保持未限定。
- **排序稳定性**：默认按模型主键排序（自定义主键、复合主键自动识别全集）；指定其他排序列时自动追加**全部主键列**作次级排序，方向跟随主排序，同列不同写法自动去重。**在数据集不变且排序键组合唯一时**翻页不重不漏。
- **PageSize 有上限**（默认 100）：该参数常来自远端请求，钳制以防无界查询。任意配置值（含 `math.MaxInt`）与任意总数（含 `math.MaxInt64`）下均不 panic、分页元信息不溢出。
- **入参错误不吞**：句柄携带的 `db.Error` 在入口即被包装返回，任何路径（含 `WithTotal` 短路）都不会静默成功；模型不可解析时透传 `parse model` 根因而非伪装成排序错误。

| 参数 | 行为 |
|------|------|
| `Page` | 从 1 开始，越界钳制到 `[1, 总页数]`；无数据时 `CurrentPage`/`TotalPage` 为 0 |
| `PageSize` | `<=0` 取默认 10；上限默认 100（`WithMaxPageSize` 调整） |
| `Sort` | 设 `WithSortMapping` 时仅接受映射键（未命中回退默认排序）；未设映射时经列名文法校验，非法回退默认排序 |
| `Order` | 仅 asc/desc（不区分大小写），其余回退 asc |

**Option（只配置分页器自身，nil 安全跳过）：**

| Option | 说明 |
|--------|------|
| `WithMaxPageSize(n)` | PageSize 上限，默认 100。上限本身是信任边界配置：调大即接受对应查询开销 |
| `WithDefaultSort(col)` | 覆盖默认排序列（默认模型主键） |
| `WithSortMapping(m)` | 外部排序键→受信任列的显式映射：防注入之余限制可排序列集合（防无索引大排序），线上推荐。`m` 不得为 nil（零键 allowlist 传空 map）；构造时做防御性复制，之后修改原 map 不影响行为 |
| `WithTotal(n)` | 调用方提供总数并跳过 count；`clause.Select{Distinct}` 与原始字符串 `Select("DISTINCT …")` 下为**必需项**（见下） |

### 受限投影（DISTINCT / GROUP BY）

这两类查询的投影与分组决定了哪些列可出现在 ORDER BY（MySQL 的 DISTINCT 约束与 `ONLY_FULL_GROUP_BY`），自动主键排序会被数据库拒绝。本包不猜测调用方的投影，改为**收窄契约 + fail fast**：

```go
page, err := paginator.Paginate[Topic](
    db.Model(&Topic{}).Select("category_id, COUNT(*) AS c").Group("category_id"),
    paginator.Params{Page: 1, PageSize: 20, Sort: "category_id"}, // 必须显式排序
) // 总数自动统计为分组数量
```

- 未提供显式排序列 → 返回 `ErrSortRequired`，且**不发出任何 SQL**；
- 提供后该排序列不做表名限定、不追加主键次级排序，与投影的兼容性由调用方保证。

**统计（Count）语义按去重写法区分**，以真实 MySQL 8 实测为准：

| 写法 | GORM Count 生成 | 总数 | 本包行为 |
|------|----------------|------|---------|
| **单列** `.Distinct("col")` | `count(DISTINCT col)` | 去重数，准确 | 自动统计 |
| `.Group("col")` / `clause.GroupBy` | `count(*) … GROUP BY`，取返回行数（`*count = tx.RowsAffected`） | 分组数量，准确 | 自动统计 |
| **多列** `.Distinct("a","b")` / `.Distinct().Select("a, b")` | `count(*)` | **总行数，非去重数** | 返回 `ErrTotalRequired` |
| `Clauses(clause.Select{Distinct: true})` | `count(*)` | **总行数，非去重数** | 返回 `ErrTotalRequired` |
| 原始字符串 `Select("DISTINCT …")` / `Select("DISTINCTROW …")` | `count(*)` | **总行数，非去重数** | 返回 `ErrTotalRequired` |

GORM 只在「选择列表恰好一项、且能切成单个字段」时才生成 `count(DISTINCT col)`。实测 25 行 / 15 个去重组合的表上，`Distinct("name", "`order`")` 的 Count 返回 **25**——若当作可靠会静默错报总数并多出一页空页。本包的判定比 GORM 更严（单项且为合法列引用，带引号/别名的写法一律按不可靠处理），只会把可靠的判成需要 `WithTotal`，不会反向放行错误总数。

> **GROUP BY 自动统计的代价**：统计查询每个分组返回一行，开销为 O(分组数) 的网络传输与扫描。高基数分组（几十万以上）请改用 `WithTotal` 自行统计。

> **写在 `Scopes` 里的 `Distinct`/`Group`/`Select` 得不到契约保护**：它们在执行阶段才物化，构建前识别不到，本包会按普通查询追加主键次级排序。结果一律是**响亮失败而非静默错误**——`Group` 触发 `ONLY_FULL_GROUP_BY` 拒绝（Error 1055），`Distinct` 与多列 `Select` 触发 Count 的扫描/类型错误（错误文本由 GORM 与 driver 给出，指向性较差）。请把它们写在链式调用上。

### 其他使用限制（如实声明）

- 非受限投影下 Count 遵循 GORM Count 语义：入参含 `Select`/`Joins` 时"总数"含义随之变化（`COUNT(col)` 跳过 NULL、has-many Join 重复计数）；不符合需求时用 `WithTotal`。
- Count 与数据查询是两条 SQL，**非一致性快照**；严格一致场景请传入事务内句柄。
- 仅 OFFSET 分页：深分页（大数据量 × 大页码）不适合高频接口，此类场景应采用游标分页（不属于本包 API）。
- `WithTotal` 传入的总数**不与实际数据核对**：值不准（缓存陈旧、算错）时会静默产出错误页数与空页，准确性由调用方负责。
- 行为**仅在 MySQL 8 上做过真实数据库验证**。其他方言的引号与 DISTINCT/GROUP BY 宽容度不同（SQLite 对 ORDER BY 不在选择列表中更宽容、Postgres 加引号后大小写敏感），使用前请自行验证。

### 测试

真实 MySQL 集成测试是**发布前置条件**（`make tag` 强制执行）——fake driver 只能验 SQL 形状，DISTINCT / `ONLY_FULL_GROUP_BY` / 保留字 / Join 同名列这几类只有真实数据库能判定：

```bash
make test-integration                                   # 默认 root:@tcp(127.0.0.1:3306)/
make test-integration ORM_TEST_DSN='user:pass@tcp(host:3306)/'
```
