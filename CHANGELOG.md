# 变更记录

本文档记录 `github.com/gtkit/ormx` 的对外可见变更。

格式参考 Keep a Changelog，版本遵循语义化版本。

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
