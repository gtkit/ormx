package ormx

// 本文件集中维护 MySQL 驱动连接配置（DSN）的构建。

import (
	"errors"
	"maps"
	"net"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// ErrAddressRequired 表示缺少连接地址：既未提供 Addr、又未同时提供 Host 与 Port，
// 或使用 unix 网络（Net=="unix"）时未用 WithAddress 指定 socket 路径。可用 errors.Is 判定。
var ErrAddressRequired = errors.New("ormx: mysql address is required")

// ErrSystemVariableNameRequired 表示系统变量名为空或纯空白字符；可用 errors.Is 判定。
var ErrSystemVariableNameRequired = errors.New("ormx: system variable name must not be empty")

// address 解析最终连接地址：Addr 优先，否则 JoinHostPort(Host, Port)。
func (c MySQLConfig) address() (string, error) {
	if c.Addr != "" {
		return c.Addr, nil
	}
	// unix 网络必须用 WithAddress 指定 socket 路径；否则默认 Host/Port 会被拼成
	// 形如 unix(127.0.0.1:3306) 的非法地址。
	if c.Net == "unix" {
		return "", ErrAddressRequired
	}
	if c.Host == "" || c.Port == "" {
		return "", ErrAddressRequired
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
	cfg.Params = maps.Clone(c.SystemVariables)
	for key := range cfg.Params {
		if strings.TrimSpace(key) == "" {
			return nil, ErrSystemVariableNameRequired
		}
	}
	cfg.ConnectionAttributes = c.ConnectionAttributes
	cfg.Collation = c.Collation
	// 仅在显式提供时覆盖，避免把驱动默认的 time.UTC 改成 nil 导致时间解析 panic。
	if c.Loc != nil {
		cfg.Loc = c.Loc
	}
	cfg.TLSConfig = c.TLSConfig
	cfg.Timeout = c.Timeout
	cfg.ReadTimeout = c.ReadTimeout
	cfg.WriteTimeout = c.WriteTimeout
	cfg.ParseTime = c.ParseTime
	return cfg, nil
}
