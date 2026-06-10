package jetorm

import (
	"context"
	"database/sql"

	jetmysql "github.com/go-jet/jet/v2/mysql"
)

type Client struct {
	db        *sql.DB
	config    Config
	ownsSQLDB bool
}

func Open(ctx context.Context, opts ...Option) (*Client, error) {
	cfg := NewConfig(opts...)

	db, err := openDBFn(cfg)
	if err != nil {
		return nil, err
	}

	applyPoolOptions(db, cfg)

	pingCtx, cancel := normalizeContext(ctx, cfg.QueryTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Client{
		db:        db,
		config:    cfg,
		ownsSQLDB: true,
	}, nil
}

func OpenWithDB(db *sql.DB, cfg Config) (*Client, error) {
	if db == nil {
		return nil, ErrNilDB
	}

	applyPoolOptions(db, cfg)

	return &Client{
		db:        db,
		config:    cfg.Clone(),
		ownsSQLDB: false,
	}, nil
}

func (c *Client) DB() *sql.DB {
	return c.db
}

func (c *Client) Config() Config {
	return c.config.Clone()
}

func (c *Client) PingContext(ctx context.Context) error {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return c.db.PingContext(queryCtx)
}

func (c *Client) Stats() sql.DBStats {
	return c.db.Stats()
}

func (c *Client) Close() error {
	if !c.ownsSQLDB || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *Client) ExecContext(ctx context.Context, stmt jetmysql.Statement) (sql.Result, error) {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return stmt.ExecContext(queryCtx, c.db)
}

func (c *Client) QueryContext(ctx context.Context, stmt jetmysql.Statement, dest any) error {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return stmt.QueryContext(queryCtx, c.db, dest)
}

func (c *Client) Rows(ctx context.Context, stmt jetmysql.Statement) (*jetmysql.Rows, error) {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return stmt.Rows(queryCtx, c.db)
}
