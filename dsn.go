package ormx

// 本文件集中维护 MySQL 驱动连接配置（DSN）的构建。

import (
	"errors"
	"maps"
	"net"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// ErrAddressRequired 表示既未提供 Addr，也未同时提供 Host 与 Port；
// 可用 errors.Is 判定 Open 因缺少连接地址而失败。
var ErrAddressRequired = errors.New("ormx: mysql address is required")

// address 解析最终连接地址：Addr 优先，否则 JoinHostPort(Host, Port)。
func (c MySQLConfig) address() (string, error) {
	if c.Addr != "" {
		return c.Addr, nil
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
