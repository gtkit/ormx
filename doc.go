// Package ormx 提供基于 GORM 的 MySQL 数据访问便捷封装：
// 连接与连接池配置、事务死锁自动重试、单机连接健康探活与可观测，以及 GORM 的 zap 日志适配。
//
// 基本用法：
//
//	client, err := ormx.Open(ctx,
//	    ormx.WithHost("127.0.0.1"),
//	    ormx.WithPort("3306"),
//	    ormx.WithDatabase("app"),
//	    ormx.WithUser("root"),
//	    ormx.WithPassword(os.Getenv("DB_PASSWORD")),
//	)
//	if err != nil {
//	    return err
//	}
//	defer client.Close()
//
//	db := client.DB() // *gorm.DB，直接走 GORM API
//
// 事务通过 [Client.WithTx] 执行，遇 MySQL 死锁（1213）或锁等待超时（1205）
// 自动按带抖动的指数退避重试。
//
// 连接健康检查与连接池指标见 [Client.HealthCheck]、[Client.StatsSnapshot] 与 [Client.Metrics]。
//
// [Client] 并发安全，可在多个 goroutine 间共享。
// GORM 的 zap 日志适配见子包 zlogger。
package ormx
