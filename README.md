# ormx

企业级 MySQL 数据访问封装（内部使用），单一活跃模块，包含两个包：

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

### 打开方式

| 入口 | 说明 |
|------|------|
| `ormx.Open(ctx, opts...)` | 按 Option 构建配置并连接，最常用 |
| `ormx.MustOpen(ctx, opts...)` | 同上，失败时 panic，适合启动期 wiring |
| `ormx.OpenWithDB(ctx, sqlDB, opts...)` | 复用已有 `*sql.DB`（连接池设置仍会应用）；`sqlDB` 所有权归调用方，`Close()` 不会关闭它 |
| `ormx.NewConfig(opts...)` / `cfg.With(opts...)` / `cfg.Open(ctx)` | 先构建 `Config` 值再打开，适合从配置文件映射、多实例复用基础配置 |

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
| `WithName(name)` | `"default"` | 实例名，体现在健康报告、指标 label、事务重试事件中；多实例时建议显式设置 |
| `WithHost(host)` | `127.0.0.1` | 主机；设置后清空 Addr |
| `WithPort(port)` | `3306` | 端口；设置后清空 Addr |
| `WithAddress(addr)` | 空 | 完整地址（`host:port`），优先级高于 Host/Port |
| `WithNetwork(network)` | `tcp` | 网络类型（如 `unix`） |
| `WithDatabase(name)` | 空 | 数据库名 |
| `WithUser(user)` | 空 | 用户名 |
| `WithPassword(password)` | 空 | 密码（日志输出自动脱敏） |
| `WithParseTime(enabled)` | `true` | 是否把 DATETIME 解析为 `time.Time` |
| `WithLocation(loc)` | `time.Local` | DSN 时区 |
| `WithTimeout(d)` | `10s` | 建连超时 |
| `WithReadTimeout(d)` | `30s` | I/O 读超时 |
| `WithWriteTimeout(d)` | `30s` | I/O 写超时 |
| `WithTLSConfig(name)` | 空 | TLS 配置名（需先用 `mysql.RegisterTLSConfig` 注册） |
| `WithCollation(collation)` | 驱动默认 | 连接 collation |
| `WithConnectionAttributes(attrs)` | 空 | 连接属性（`performance_schema.session_connect_attrs`） |
| `WithSystemVariable(key, value)` | — | 追加连接系统变量，连接后执行 `SET key = value`；value 须是合法 SQL 表达式。**非** DSN 内置参数（charset/loc/parseTime 等有专用 Option） |
| `WithSystemVariables(params)` | — | 批量追加连接系统变量，语义同上 |

#### 连接池

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithMaxOpenConns(n)` | `50` | 最大打开连接数 |
| `WithMaxIdleConns(n)` | `10` | 最大空闲连接数 |
| `WithConnMaxLifetime(d)` | `30m` | 连接最大存活时间 |
| `WithConnMaxIdleTime(d)` | `10m` | 连接最大空闲时间 |

#### GORM 行为

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithGormLogger(log)` | GORM 默认 Warn | 设置任意 `gormlogger.Interface` 实现；未设置时输出错误 SQL 与超过 200ms 的慢 SQL |
| `WithZlogger(opts...)` | 无参为 no-op（静默） | 一步注入 zap 日志器，等价 `WithGormLogger(zlogger.New(opts...))`；不传 Option 时用 zlogger 默认（no-op logger，静默丢弃，**非** GORM 默认 Warn），须至少 `zlogger.WithLogger(...)` 注入 zap。详见下文 zlogger 章节 |
| `WithPrepareStmt(enabled)` | `false` | 开启 PreparedStatement 缓存 |
| `WithPrepareStmtCache(maxSize, ttl)` | 不限制 | PreparedStatement 缓存容量与 TTL |
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

未传 `WithGormLogger` 时，GORM 使用默认 Warn 日志器向 stdout 输出错误 SQL 与超过 200ms 的慢 SQL，不记录正常快查询。日志通过 `gormlogger.Interface` 控制，不需要额外布尔开关：

```go
// 完全关闭 SQL 日志
ormx.WithGormLogger(gormlogger.Discard)

// 记录错误 SQL 与慢 SQL（GORM 默认行为）
ormx.WithGormLogger(gormlogger.Default.LogMode(gormlogger.Warn))

// 记录全部 SQL，仅建议开发环境使用
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
| `WithServerVersion(version)` | 自动探测 | 手工指定服务端版本 |
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
| `Stats() sql.DBStats` / `StatsSnapshot()` | 连接池统计 |
| `HealthCheck(ctx) HealthReport` | 健康检查（Ping + 自定义探针，默认 5s 超时） |
| `Metrics() []MetricSample` | 连接池指标采样（`orm_db_*` 系列，带 name label） |
| `WithTx` / `WithReadTx` | 事务，见下节 |
| `Close() error` | 关闭连接池（`OpenWithDB` 包装的实例不关闭外部 `*sql.DB`） |

### 事务（死锁自动重试）

```go
err := client.WithTx(ctx, nil, func(tx *gorm.DB) error {
    if err := tx.Create(&order).Error; err != nil {
        return err
    }
    return tx.Model(&stock).Update("count", gorm.Expr("count - ?", 1)).Error
})
```

- `fn` 返回 nil 则提交，返回 error 则回滚；panic 时回滚后继续抛出。
- 遇到 MySQL 死锁（1213）或锁等待超时（1205）时自动按带抖动的指数退避重试，**默认最多 3 次**。重试意味着 `fn` 可能执行多次，事务内逻辑须幂等。
- 第二个参数可传 `*sql.TxOptions` 指定隔离级别/只读；`WithReadTx(ctx, fn)` 是 `ReadOnly: true` 的便捷形式。

每次调用可用 `TxOption` 覆盖重试行为：

| TxOption | 默认值 | 说明 |
|----------|--------|------|
| `WithMaxRetries(n)` | `3` | 最大重试次数，`0` 禁用重试 |
| `WithRetryBaseWait(d)` | `5ms` | 退避基础等待 |
| `WithRetryMaxWait(d)` | `50ms` | 单次退避上限 |

```go
err := client.WithTx(ctx, nil, fn, ormx.WithMaxRetries(5), ormx.WithRetryMaxWait(200*time.Millisecond))
```

### 多库多实例

配置按实例隔离（没有全局状态），每次 `Open` 返回独立的 `*Client`，各自持有独立连接池。连接多个库就是创建多个实例，按依赖注入交给各业务模块：

```go
orderDB, err := ormx.Open(ctx, ormx.WithDatabase("orders"), ormx.WithName("orders") /* ... */)
userDB, err  := ormx.Open(ctx, ormx.WithDatabase("users"), ormx.WithName("users") /* ... */)

orderRepo := repo.NewOrderRepo(orderDB.DB())
userRepo  := repo.NewUserRepo(userDB.DB())
```

### 健康检查与指标

单机 Client 提供健康检查和 Prometheus 风格的指标采样：

```go
report := client.HealthCheck(ctx)
if !report.Healthy() {
    log.Printf("db down: %v", report.Error)
}

for _, m := range client.Metrics() {
    // m.Name 形如 orm_db_open_connections / orm_db_wait_count_total ...
    // m.Labels 含 name（实例名）
    gauge.With(m.Labels).Set(m.Value)
}
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
| `ormx.ErrAddressRequired` | 既未提供 `Addr`、又缺 `Host` 或 `Port` |
| `ormx.ErrNilSQLDB` | 向 `OpenWithDB` 传入 nil `*sql.DB` |
| `ormx.ErrNilTxFunc` | 向 `WithTx` 传入 nil 事务函数 |

```go
if _, err := ormx.Open(ctx, /* ...缺少地址... */); errors.Is(err, ormx.ErrAddressRequired) {
    // 配置缺少连接地址
}
```

---

## zlogger（GORM 的 zap 日志适配）

`zlogger` 是实现 `gormlogger.Interface` 的 zap 日志器。用 `ormx.WithZlogger` 一步接入，无需显式调用 `zlogger.New`：

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
    ormx.WithZlogger(
        zlogger.WithLogger(zlog),
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

`WithZlogger(opts...)` 等价于 `WithGormLogger(zlogger.New(opts...))`。需要注入自定义 `gormlogger.Interface` 实现（或已构造好的日志器）时，仍用 `WithGormLogger`。

### 选项函数

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithLogger(log)` | nop（不输出） | 底层 `*zap.Logger`；**不设置则所有日志静默丢弃**，必须传入 |
| `WithLogLevel(level)` | `gormlogger.Warn` | 日志级别（Silent / Error / Warn / Info） |
| `WithSlowThreshold(d)` | `200ms` | 慢查询阈值；执行耗时超过即按 Warn 输出 `gorm slow query`，设为 `0` 关闭慢查询日志 |
| `WithIgnoreRecordNotFoundError(enabled)` | `false` | 忽略 `gorm.ErrRecordNotFound`，不作为错误日志输出 |
| `WithParameterizedQueries(enabled)` | `false`（兼容默认） | 开启后日志中的 SQL 不带参数值（脱敏），只输出占位符语句；生产环境建议设为 `true` |
| `WithTraceIDExtractor(fn)` | 无 | 从 context 提取 trace/request id，附加为 `trace_id` 字段，串联 SQL 日志与请求链路 |

### 输出行为

每条 SQL 日志包含字段：`source`（调用位置）、`elapsed`（耗时）、`sql`、`rows`（影响行数，-1 时省略）、`trace_id`（配置了 extractor 且能提取到时）。按以下优先级输出：

1. 执行出错（且未被 RecordNotFound 忽略）→ `Error` 级 `gorm query error`，附 `error` 字段
2. 耗时超过慢查询阈值 → `Warn` 级 `gorm slow query`，附 `slow_threshold` 字段
3. 日志级别为 Info → `Info` 级 `gorm query`（全量 SQL 日志，仅建议开发环境开启）

`LogMode` 遵循 GORM 约定返回调级别后的副本，可配合 `db.Session(&gorm.Session{Logger: ...})` 做局部调级。

> 安全提示：`WithParameterizedQueries(false)` 会保留 SQL 绑定参数，可能把密码、Token 或其他敏感值写入日志，仅应在确认数据安全的受控排障环境使用。

---

## 发版

```bash
make tag             # patch 发版：自动 bump patch、跑门禁、打 tag 并推送
make tag BUMP=minor  # minor 发版（新增向后兼容功能，或按本项目策略承载破坏性变更）
```

本项目只维护 `v1`，不发 `major`/`v2`（`make tag BUMP=major` 会被拒绝）；破坏性变更按 MINOR 发布并在 `CHANGELOG.md` 以 **⚠ 破坏性变更** 标注。

发版前提：工作区干净，且 `CHANGELOG.md` 已有目标版本条目（格式 `## [vX.Y.Z] - YYYY-MM-DD`）。
门禁包含 vet、lint、race 测试、benchmark、覆盖率 ≥ 80% 与 govulncheck，任一失败即中止；
tag message 自动携带该版本的 CHANGELOG 内容。
