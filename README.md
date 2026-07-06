# ormx

企业级 MySQL 数据访问封装（内部使用），单一活跃模块，包含两个包：

| 包 | 用途 |
|----|------|
| `github.com/gtkit/ormx` | 基于 GORM 的客户端——连接管理、集群读写分离、健康探活、事务死锁自动重试、写后读一致性窗口 |
| `github.com/gtkit/ormx/zlogger` | GORM 的 zap 日志适配——慢查询阈值、trace id 提取、SQL 参数脱敏 |

> 面向 go-jet 的 SQL-first 封装已分离为独立模块 [`github.com/gtkit/jetx`](https://github.com/gtkit/jetx)。

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

`Config` 是纯值类型：`With` 返回深拷贝后的新配置，不修改原值；`Config.String()` / `%#v` 输出自动把密码脱敏为 `******`，可放心打日志。`cfg.RedactedDSN()` 返回脱敏后的 DSN 字符串。

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
| `WithDSNParam(key, value)` | — | 追加单个自定义 DSN 参数（如 `charset`） |
| `WithDSNParams(params)` | — | 批量追加 DSN 参数 |

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
| `WithGormLogger(log)` | GORM 默认 | 设置 `gormlogger.Interface`，通常配合 `zlogger.New(...)` 使用 |
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

#### 启动与健康

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithStartupPing(enabled)` | `true` | Open 时先 Ping 验证连通性 |
| `WithStartupPingRetry(maxRetries, baseWait, maxWait)` | `0, 1s, 5s` | 启动 Ping 失败后的重试次数与退避区间 |
| `WithHealthProbe(probe)` | 无 | 自定义健康探针，在 Ping 通过后追加执行（如检查只读标记、复制延迟） |

#### 事务观测

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithTxRetryObserver(observer)` | 无 | 每次死锁重试前回调 `TxRetryEvent`（实例名、第几次、等待时长、错误），用于打点告警 |

#### MySQL Dialect（少用，对接非标准部署时才需要）

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithDriverName(name)` | `mysql` | 自定义驱动名 |
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
| `Config() Config` | 配置快照（深拷贝） |
| `Name() string` | 实例名（未设置时为 `default`） |
| `PingContext(ctx) error` | 连通性检查 |
| `Stats() sql.DBStats` / `StatsSnapshot()` | 连接池统计 |
| `HealthCheck(ctx) HealthReport` | 健康检查（Ping + 自定义探针，默认 5s 超时） |
| `Metrics() []MetricSample` | 连接池指标采样（`orm_db_*` 系列，带 name/role label） |
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

### 集群（读写分离 / 故障切换）

把一个主库和若干副本组成 `Cluster`：写请求路由到主库，读请求在健康副本间轮询，副本全挂时可回退主库。

```go
primary, _ := ormx.Open(ctx, ormx.WithName("primary") /* ... */)
replica, _ := ormx.Open(ctx, ormx.WithName("replica-1") /* ... */)

cluster, err := ormx.NewCluster(primary, replica)
if err != nil {
    return err
}
defer cluster.Close() // 统一关闭全部节点

// 周期健康巡检：探活失败的节点标记 down，恢复后自动回到读池。
// RunHealthLoop 返回 error（interval ≤ 0 或集群已关闭），自起 goroutine 时需显式处理
go func() { _ = cluster.RunHealthLoop(ctx, 10*time.Second) }()

writeClient, err := cluster.WriteClient()      // 主库
readClient, err := cluster.ReaderClientCtx(ctx) // 副本轮询，感知写后读窗口
```

也可以直接用配置一步打开（副本并行建连）：

```go
cluster, err := ormx.OpenClusterWithOptions(ctx, primaryCfg, []ormx.Config{replicaCfg1, replicaCfg2},
    ormx.WithHealthCheckTimeout(3*time.Second),
)
```

#### ClusterOption

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithReadFallbackToPrimary(enabled)` | `true` | 所有副本不可读时读请求回退主库 |
| `WithAutoRecoverReplicas(enabled)` | `true` | 健康巡检中 Ping 恢复的 down 副本自动回到读池 |
| `WithHealthCheckTimeout(d)` | `5s` | 健康检查 Ping 的默认超时；调用方 context 有更短 deadline 时优先 |

#### Cluster 常用方法

| 方法 | 说明 |
|------|------|
| `WriteClient() (*Client, error)` | 取主库客户端，主库不可用时返回错误 |
| `ReaderClient() (*Client, error)` | 在 ready 副本间轮询取读客户端 |
| `ReaderClientCtx(ctx) (*Client, error)` | 同上，但 ctx 带写标记时强制路由主库（写后读一致性） |
| `MustWriteDB()` / `MustReadDB()` | 直接取 `*gorm.DB`，不可用时 panic，仅适合启动期 wiring |
| `WithTx(ctx, fn, txOpts...)` | 在主库上执行事务（含死锁重试） |
| `WithReadTx(ctx, fn)` | 在读节点上执行只读事务（感知写标记） |
| `HealthCheck(ctx)` | 并行探活所有节点，返回 `ClusterHealthReport`（up / degraded / down） |
| `Refresh(ctx)` | 探活并更新节点状态（`RunHealthLoop` 内部周期调用的就是它） |
| `RunHealthLoop(ctx, interval)` | 周期巡检，ctx 取消时退出；由调用方自起 goroutine |
| `DrainReplica(name, cause)` | 把副本标记为 draining，摘出读池（发版、维护窗口用）。注意非长期粘性：副本探活失败转 down 后，恢复时会被健康巡检自动拉回读池；长期摘除需暂停健康循环 |
| `RecoverReplica(ctx, name)` | Ping 通过后把副本恢复为 ready |
| `MarkPrimaryDown(cause)` | 把主库标记 down（注意：后续 Refresh Ping 成功会自动恢复 Ready） |
| `SwitchPrimary(ctx, name)` | 把指定副本提升为主库，旧主库降级为 draining 副本 |
| `Nodes()` / `PrimaryNode()` / `ReplicaNodes()` | 节点状态快照 |
| `Metrics()` | 全部节点的连接池指标 |
| `Close()` | 关闭所有节点（去重，共享 Client 只关一次） |

`WriteDB()` / `ReadDB()` / `ReadDBCtx()` 已标记 Deprecated（不可用时返回 nil，易引发空指针），新代码请用对应的 `*Client` 版本。

#### 路由错误判断

集群读写路由失败可用 `errors.Is` 区分类型：

| 错误 | 含义 |
|------|------|
| `ormx.ErrNoReadableNode` | 没有可读节点（副本全部不可读，且回退主库被关闭或主库也不可读） |
| `ormx.ErrPrimaryUnavailable` | 主库不可用（不存在或处于 down 状态） |
| `ormx.ErrClusterClosed` | 集群已关闭 |

#### 写后读一致性

主从复制存在延迟，"写完立刻读"可能从副本读到旧数据。在写入成功后给 context 打写标记，后续读请求即被路由到主库：

```go
// 方式一：本次请求内一直读主库
ctx = ormx.ContextWithWriteFlag(ctx)

// 方式二：只在一个时间窗口内读主库，窗口过期自动恢复读副本（推荐）
ctx = ormx.ContextWithWriteWindow(ctx, 500*time.Millisecond)

readClient, err := cluster.ReaderClientCtx(ctx) // 命中写标记 → 主库
```

| 函数 | 说明 |
|------|------|
| `ContextWithWriteFlag(ctx)` | 标记写入，后续读路由主库（无过期） |
| `ContextWithWriteWindow(ctx, ttl)` | 同上，但 ttl 过期后自动失效；ttl ≤ 0 等价于清除标记 |
| `ContextClearWriteFlag(ctx)` | 清除写标记 |
| `HasWriteFlag(ctx) bool` | 查询 ctx 当前是否带有效写标记 |

### 健康检查与指标

单实例与集群均提供健康检查和 Prometheus 风格的指标采样：

```go
report := client.HealthCheck(ctx)
if !report.Healthy() {
    log.Printf("db down: %v", report.Error)
}

for _, m := range client.Metrics() {
    // m.Name 形如 orm_db_open_connections / orm_db_wait_count_total ...
    // m.Labels 含 name（实例名）与 role（standalone/primary/replica）
    gauge.With(m.Labels).Set(m.Value)
}
```

`WithHealthProbe` 可在 Ping 之外追加业务探针，例如校验副本只读：

```go
ormx.WithHealthProbe(func(ctx context.Context, c *ormx.Client, role ormx.NodeRole) error {
    if role != ormx.RoleReplica {
        return nil
    }
    var readOnly int
    return c.DB().WithContext(ctx).Raw("SELECT @@read_only").Scan(&readOnly).Error
})
```

---

## zlogger（GORM 的 zap 日志适配）

`zlogger.New` 返回一个实现 `gormlogger.Interface` 的日志器，通过 `ormx.WithGormLogger` 接入：

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
    ormx.WithGormLogger(zlogger.New(
        zlogger.WithLogger(zlog),
        zlogger.WithLogLevel(gormlogger.Warn),
        zlogger.WithSlowThreshold(300*time.Millisecond),
        zlogger.WithIgnoreRecordNotFoundError(true),
        zlogger.WithTraceIDExtractor(func(ctx context.Context) string {
            if id, ok := ctx.Value("X-Request-ID").(string); ok {
                return id
            }
            return ""
        }),
    )),
)
```

### 选项函数

| Option | 默认值 | 说明 |
|--------|--------|------|
| `WithLogger(log)` | nop（不输出） | 底层 `*zap.Logger`；**不设置则所有日志静默丢弃**，必须传入 |
| `WithLogLevel(level)` | `gormlogger.Warn` | 日志级别（Silent / Error / Warn / Info） |
| `WithSlowThreshold(d)` | `200ms` | 慢查询阈值；执行耗时超过即按 Warn 输出 `gorm slow query`，设为 `0` 关闭慢查询日志 |
| `WithIgnoreRecordNotFoundError(enabled)` | `false` | 忽略 `gorm.ErrRecordNotFound`，不作为错误日志输出 |
| `WithParameterizedQueries(enabled)` | `false` | 开启后日志中的 SQL 不带参数值（脱敏），只输出占位符语句 |
| `WithTraceIDExtractor(fn)` | 无 | 从 context 提取 trace/request id，附加为 `trace_id` 字段，串联 SQL 日志与请求链路 |

### 输出行为

每条 SQL 日志包含字段：`source`（调用位置）、`elapsed`（耗时）、`sql`、`rows`（影响行数，-1 时省略）、`trace_id`（配置了 extractor 且能提取到时）。按以下优先级输出：

1. 执行出错（且未被 RecordNotFound 忽略）→ `Error` 级 `gorm query error`，附 `error` 字段
2. 耗时超过慢查询阈值 → `Warn` 级 `gorm slow query`，附 `slow_threshold` 字段
3. 日志级别为 Info → `Info` 级 `gorm query`（全量 SQL 日志，仅建议开发环境开启）

`LogMode` 遵循 GORM 约定返回调级别后的副本，可配合 `db.Session(&gorm.Session{Logger: ...})` 做局部调级。

---

## 发版

```bash
make tag             # patch 发版：自动 bump patch、跑门禁、打 tag 并推送
make tag BUMP=minor  # minor 发版（新增向后兼容的功能时）
make tag BUMP=major  # major 发版（破坏性变更时）
```

发版前提：工作区干净，且 `CHANGELOG.md` 已有目标版本条目（格式 `## [vX.Y.Z] - YYYY-MM-DD`）。
门禁包含 vet、lint、race 测试、benchmark、覆盖率 ≥ 80% 与 govulncheck，任一失败即中止；
tag message 自动携带该版本的 CHANGELOG 内容。

> 注意：升 major 到 v2 及以上时，必须先把 `go.mod` 的 module path 改为 `github.com/gtkit/ormx/v2`
> 并同步包内 import（Go Module 硬要求），`make tag BUMP=major` 不会自动处理这一步。
