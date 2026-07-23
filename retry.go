package ormx

// 本文件集中维护事务与启动 Ping 的重试辅助：MySQL 死锁判定与带抖动的指数退避。

import (
	"errors"
	"math/rand/v2"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	mysqlErrDeadlock = 1213
	mysqlErrLockWait = 1205
)

// isDeadlock 判断错误是否属于 MySQL 死锁（1213）或锁等待超时（1205）。
func isDeadlock(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == mysqlErrDeadlock || mysqlErr.Number == mysqlErrLockWait
}

// retryBackoff 返回带抖动的指数退避时长。
// 公式：min(baseWait * 2^attempt + jitter, maxWait)，抖动最多 50%。
func retryBackoff(attempt int, baseWait, maxWait time.Duration) time.Duration {
	wait := baseWait << attempt // baseWait * 2^attempt
	// attempt 极大时左移溢出会回绕成负数或 0，与已达上限一样直接返回 maxWait，
	// 避免 rand.Int64N 收到非正参数 panic。
	if wait <= 0 || wait >= maxWait {
		return maxWait
	}
	const jitterDivisor = 2
	// #nosec G404 -- 退避抖动非安全用途，math/rand/v2 足够
	jitter := time.Duration(rand.Int64N(int64(wait/jitterDivisor) + 1))
	return min(wait+jitter, maxWait)
}
