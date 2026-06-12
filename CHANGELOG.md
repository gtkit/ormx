# 变更记录

本文档记录 `github.com/gtkit/ormx` 的对外可见变更。

格式参考 Keep a Changelog，版本遵循语义化版本。

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
