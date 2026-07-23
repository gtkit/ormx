package ormx

import (
	"context"
	"database/sql"
	"fmt"

	"gorm.io/gorm"
)

// Client 封装单个数据库连接，持有 GORM 实例与底层 *sql.DB。
// Client 自身状态只读，可在多个 goroutine 间并发使用；但调用方注入的
// Logger、HealthProbe、TxRetryObserver 会在并发下被调用，其并发安全由调用方保证。
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

// Config 返回客户端配置的脱敏快照：深拷贝后把密码、连接参数值与连接属性替换为占位符，
// 不含明文凭据或敏感绑定值，仅供检视。需要真实值请由调用方保留原始配置。
func (c *Client) Config() Config {
	cfg := c.config.Clone()
	cfg.MySQL = cfg.MySQL.redacted()
	return cfg
}

// PingContext 检测数据库连接是否可用。
func (c *Client) PingContext(ctx context.Context) error {
	if err := c.sqlDB.PingContext(normalizeContext(ctx)); err != nil {
		return fmt.Errorf("ormx: ping: %w", err)
	}
	return nil
}

// Stats 返回底层连接池的统计信息。
func (c *Client) Stats() sql.DBStats {
	return c.sqlDB.Stats()
}

// Close 关闭底层 *sql.DB。仅当 Client 拥有该连接时才真正关闭，否则直接返回 nil。
func (c *Client) Close() error {
	// 先释放 GORM 预编译语句缓存（启用 WithPrepareStmt 时存在）。即使不拥有 sqlDB
	// （OpenWithDB 场景），该缓存也由本 Client 持有，必须关闭以释放服务端预编译语句。
	if c.db != nil {
		if psdb, ok := c.db.ConnPool.(*gorm.PreparedStmtDB); ok {
			psdb.Close()
		}
	}
	if !c.ownsSQLDB || c.sqlDB == nil {
		return nil
	}
	if err := c.sqlDB.Close(); err != nil {
		return fmt.Errorf("ormx: close sql db: %w", err)
	}
	return nil
}
