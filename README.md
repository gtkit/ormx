# ormx

企业级 MySQL 数据访问封装（内部使用），单一活跃模块：

- 根包 `ormx`：基于 GORM 的客户端——连接管理、集群读写分离、健康探活、事务死锁自动重试、写后读一致性窗口
- `ormx/jetorm`：面向 [go-jet](https://github.com/go-jet/jet) 的 SQL-first 封装——连接、连接池、事务与超时治理，不重新抽象 go-jet 的查询 DSL
- `ormx/zlogger`：GORM 的 zap 日志适配（慢查询阈值、trace id 提取）

本模块取代 `github.com/gtkit/orm`（v1/v2/jetorm 均已冻结），见文末迁移指引。

## 安装

```bash
go get github.com/gtkit/ormx
```

## 快速开始

```go
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

db := client.DB() // *gorm.DB
```

## 多库多实例

配置是按实例隔离的（没有全局状态），每次 `Open` 返回独立的 `*Client`，各自持有独立连接池。连接多个库就是创建多个实例，然后按依赖注入的方式交给各业务模块：

```go
orderDB, err := ormx.Open(ctx, ormx.WithDatabase("orders"), /* ... */)
userDB, err  := ormx.Open(ctx, ormx.WithDatabase("users"), ormx.WithName("users"), /* ... */)

// 注入到项目（构造函数注入 / wire / fx 均可）
orderRepo := repo.NewOrderRepo(orderDB.DB())
userRepo  := repo.NewUserRepo(userDB.DB())
```

`WithName` 给实例命名，会体现在指标与事务重试观测事件中，多实例时建议显式设置。

## 集群（读写分离 / 故障切换）

```go
primary, _ := ormx.Open(ctx, ormx.WithName("primary"), /* ... */)
replica, _ := ormx.Open(ctx, ormx.WithName("replica-1"), /* ... */)

cluster, err := ormx.NewCluster(primary, replica)
defer cluster.Close()

writeDB := cluster.MustWriteDB()
readDB  := cluster.ReadDBCtx(ctx) // 自动感知写后读窗口

go cluster.RunHealthLoop(ctx, 10*time.Second)
```

写后读一致性：`ormx.ContextWithWriteWindow(ctx, ttl)` 标记写入后的读请求路由到主库，窗口过期自动恢复读副本。

## 事务

```go
err := client.WithTx(ctx, nil, func(tx *gorm.DB) error {
    // ...
    return nil
})
```

死锁（1213）与锁等待超时（1205）默认自动重试（带抖动退避），可用 `WithMaxRetries` / `WithRetryBaseWait` / `WithRetryMaxWait` 调整，`WithTxRetryObserver` 观测重试事件。

## go-jet 用法（ormx/jetorm）

```go
client, err := jetorm.Open(ctx,
    jetorm.WithHost("127.0.0.1"),
    jetorm.WithPort("3306"),
    jetorm.WithDatabase("app"),
    jetorm.WithUser("root"),
    jetorm.WithPassword("secret"),
    jetorm.WithQueryTimeout(30*time.Second), // 单条语句超时
    jetorm.WithTxTimeout(2*time.Minute),     // 事务总时长上限（可选）
    jetorm.WithTxRetry(3, 0, 0),             // 死锁自动重试（可选，默认关闭）
)
defer client.Close()

stmt := table.Users.SELECT(table.Users.AllColumns).WHERE(table.Users.ID.EQ(jetmysql.Int(1)))
var dest []model.Users
err = client.QueryContext(ctx, stmt, &dest)
```

与 gorm 客户端共享连接池：`jetorm.OpenWithDB(client.SQLDB(), cfg)`。

## 自 github.com/gtkit/orm 迁移

路径替换：

| 旧 | 新 |
|----|----|
| `github.com/gtkit/orm/v2` | `github.com/gtkit/ormx` |
| `github.com/gtkit/orm/v2/zlogger` | `github.com/gtkit/ormx/zlogger` |
| `github.com/gtkit/orm/jetorm` | `github.com/gtkit/ormx/jetorm` |

根包 API 与 `orm/v2`（v2.3.1）等价，仅包名由 `orm` 改为 `ormx`。

**jetorm 行为差异**（相对旧 `orm/jetorm`）：

1. `QueryTimeout` 不再隐式限制整个事务的生命周期，仅作用于单条语句；事务总时长改由 `WithTxTimeout` 显式控制。依赖旧行为者需补设 `WithTxTimeout`。
2. 新增 `WithDSNParam`、`WithDialTimeout`、`WithReadTimeout`、`WithWriteTimeout`、`WithLoc`，原硬编码值（10s/30s/30s、`time.Local`）转为默认值。
3. 新增 `WithTxRetry(maxRetries, baseWait, maxWait)` 死锁自动重试，默认关闭；开启后事务函数可能执行多次，须保证幂等。
4. `Config` 新增 `Params`、`Loc`、各超时与事务相关字段；`Clone` 现深拷贝 `Params`。其余字段与行为不变。

## 发版

```bash
make tag   # 自动 bump patch、打 tag 并推送；要求工作区干净
```
