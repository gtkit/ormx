package ormx

import (
	"context"
	"database/sql"

	"gorm.io/gorm"
)

type Client struct {
	db        *gorm.DB
	sqlDB     *sql.DB
	config    Config
	ownsSQLDB bool
}

func (c *Client) DB() *gorm.DB {
	return c.db
}

func (c *Client) SQLDB() *sql.DB {
	return c.sqlDB
}

func (c *Client) Config() Config {
	return c.config.Clone()
}

func (c *Client) PingContext(ctx context.Context) error {
	return c.sqlDB.PingContext(normalizeContext(ctx))
}

func (c *Client) Stats() sql.DBStats {
	return c.sqlDB.Stats()
}

func (c *Client) Close() error {
	if !c.ownsSQLDB || c.sqlDB == nil {
		return nil
	}
	return c.sqlDB.Close()
}
