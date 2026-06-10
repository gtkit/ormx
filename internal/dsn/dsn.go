// Package dsn 集中维护 MySQL 驱动配置构建、连接池默认值与重试辅助，
// 供根包与 jetorm 共用，保证两个消费方行为一致。
package dsn

import (
	"errors"
	"maps"
	"math/rand/v2"
	"net"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// 连接与连接池默认值。
const (
	DefaultDialTimeout     = 10 * time.Second
	DefaultReadTimeout     = 30 * time.Second
	DefaultWriteTimeout    = 30 * time.Second
	DefaultMaxOpenConns    = 50
	DefaultMaxIdleConns    = 10
	DefaultConnMaxLifetime = 30 * time.Minute
	DefaultConnMaxIdleTime = 10 * time.Minute
)

const (
	mysqlErrDeadlock = 1213
	mysqlErrLockWait = 1205
)

// ErrAddressRequired 表示既未提供 Addr，也未同时提供 Host 与 Port。
var ErrAddressRequired = errors.New("ormx: mysql address is required")

// Params 描述驱动层连接设置。Addr 优先于 Host/Port。
type Params struct {
	User                 string
	Password             string
	Net                  string
	Host                 string
	Port                 string
	Addr                 string
	Database             string
	Params               map[string]string
	ConnectionAttributes string
	Collation            string
	Loc                  *time.Location
	TLSConfig            string
	Timeout              time.Duration
	ReadTimeout          time.Duration
	WriteTimeout         time.Duration
	ParseTime            bool
}

// Address 解析最终连接地址：Addr 优先，否则 JoinHostPort(Host, Port)。
func (p Params) Address() (string, error) {
	if p.Addr != "" {
		return p.Addr, nil
	}
	if p.Host == "" || p.Port == "" {
		return "", ErrAddressRequired
	}
	return net.JoinHostPort(p.Host, p.Port), nil
}

// DriverConfig 构建 go-sql-driver 的连接配置。
func (p Params) DriverConfig() (*mysqldriver.Config, error) {
	if p.Net == "" {
		p.Net = "tcp"
	}

	addr, err := p.Address()
	if err != nil {
		return nil, err
	}

	cfg := mysqldriver.NewConfig()
	cfg.User = p.User
	cfg.Passwd = p.Password
	cfg.Net = p.Net
	cfg.Addr = addr
	cfg.DBName = p.Database
	cfg.Params = maps.Clone(p.Params)
	cfg.ConnectionAttributes = p.ConnectionAttributes
	cfg.Collation = p.Collation
	cfg.Loc = p.Loc
	cfg.TLSConfig = p.TLSConfig
	cfg.Timeout = p.Timeout
	cfg.ReadTimeout = p.ReadTimeout
	cfg.WriteTimeout = p.WriteTimeout
	cfg.ParseTime = p.ParseTime
	return cfg, nil
}

// IsDeadlock 判断错误是否属于 MySQL 死锁（1213）或锁等待超时（1205）。
func IsDeadlock(err error) bool {
	var mysqlErr *mysqldriver.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == mysqlErrDeadlock || mysqlErr.Number == mysqlErrLockWait
}

// RetryBackoff 返回带抖动的指数退避时长。
// 公式：min(baseWait * 2^attempt + jitter, maxWait)，抖动最多 50%。
func RetryBackoff(attempt int, baseWait, maxWait time.Duration) time.Duration {
	wait := baseWait << attempt // baseWait * 2^attempt
	const jitterDivisor = 2
	jitter := time.Duration(rand.Int64N(int64(wait/jitterDivisor) + 1)) //nolint:gosec // jitter for backoff, not security
	wait += jitter
	if wait > maxWait {
		return maxWait
	}
	return wait
}
