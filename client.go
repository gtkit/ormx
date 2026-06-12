package ormx

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

// Client 封装单个数据库连接，持有 GORM 实例与底层 *sql.DB。
// Client 并发安全，可在多个 goroutine 间共享。
type Client struct {
	db        *gorm.DB
	sqlDB     *sql.DB
	config    Config
	ownsSQLDB bool
}

// DB 返回底层 *gorm.DB。
func (c *Client) DB() *gorm.DB {
	return c.db
}

// SQLDB 返回底层 *sql.DB。
func (c *Client) SQLDB() *sql.DB {
	return c.sqlDB
}

// Config 返回客户端配置的副本。
func (c *Client) Config() Config {
	return c.config.Clone()
}

// PingContext 检测数据库连接是否可用。
func (c *Client) PingContext(ctx context.Context) error {
	return c.sqlDB.PingContext(normalizeContext(ctx))
}

// Stats 返回底层连接池的统计信息。
func (c *Client) Stats() sql.DBStats {
	return c.sqlDB.Stats()
}

// Close 关闭底层 *sql.DB。仅当 Client 拥有该连接时才真正关闭，否则直接返回 nil。
func (c *Client) Close() error {
	if !c.ownsSQLDB || c.sqlDB == nil {
		return nil
	}
	return c.sqlDB.Close()
}
