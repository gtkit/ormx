// Package ormx 提供基于 GORM 的企业级 MySQL 数据访问封装：
// 连接管理、集群读写分离、健康探活、事务死锁自动重试与写后读一致性窗口。
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
// 读写分离由 [Cluster] 提供：写请求路由主库，读请求在健康副本间轮询；
// 写入后用 [ContextWithWriteWindow] 打写标记，可在时间窗口内保证写后读一致性。
//
// [Client] 与 [Cluster] 均并发安全，可在多个 goroutine 间共享。
// GORM 的 zap 日志适配见子包 zlogger。
package ormx
