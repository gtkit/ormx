package ormx

// 本文件集中维护 MySQL 驱动配置构建与事务重试辅助（死锁判定、指数退避）。

import (
	"errors"
	"maps"
	"math/rand/v2"
	"net"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

const (
	mysqlErrDeadlock = 1213
	mysqlErrLockWait = 1205
)

// errAddressRequired 表示既未提供 Addr，也未同时提供 Host 与 Port。
var errAddressRequired = errors.New("ormx: mysql address is required")

// address 解析最终连接地址：Addr 优先，否则 JoinHostPort(Host, Port)。
func (c MySQLConfig) address() (string, error) {
	if c.Addr != "" {
		return c.Addr, nil
	}
	if c.Host == "" || c.Port == "" {
		return "", errAddressRequired
	}
	return net.JoinHostPort(c.Host, c.Port), nil
}

// driverConfig 构建 go-sql-driver 的连接配置。
func (c MySQLConfig) driverConfig() (*mysqldriver.Config, error) {
	network := c.Net
	if network == "" {
		network = "tcp"
	}

	addr, err := c.address()
	if err != nil {
		return nil, err
	}

	cfg := mysqldriver.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	cfg.Net = network
	cfg.Addr = addr
	cfg.DBName = c.Database
	cfg.Params = maps.Clone(c.Params)
	cfg.ConnectionAttributes = c.ConnectionAttributes
	cfg.Collation = c.Collation
	cfg.Loc = c.Loc
	cfg.TLSConfig = c.TLSConfig
	cfg.Timeout = c.Timeout
	cfg.ReadTimeout = c.ReadTimeout
	cfg.WriteTimeout = c.WriteTimeout
	cfg.ParseTime = c.ParseTime
	return cfg, nil
}

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
	jitter := time.Duration(rand.Int64N(int64(wait/jitterDivisor) + 1)) //nolint:gosec // jitter for backoff, not security
	return min(wait+jitter, maxWait)
}
