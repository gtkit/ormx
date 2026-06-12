package jetorm

import (
	"context"
	"database/sql"

	jetmysql "github.com/go-jet/jet/v2/mysql"
)

// Client 基于 go-jet 的 MySQL 客户端，持有 *sql.DB 连接池与配置，
// 提供语句执行、事务包装与连接生命周期管理。方法可安全并发调用。
type Client struct {
	db        *sql.DB
	config    Config
	ownsSQLDB bool
}

// Open 按 Option 构建配置，打开 MySQL 连接池并执行一次 Ping 校验连通性
// （Ping 受 QueryTimeout 约束），失败时关闭连接并返回错误。
// 由 Open 创建的 *sql.DB 归 Client 所有，Close 时会一并关闭。
func Open(ctx context.Context, opts ...Option) (*Client, error) {
	cfg := NewConfig(opts...)

	db, err := openDBFn(cfg)
	if err != nil {
		return nil, err
	}

	applyPoolOptions(db, cfg)

	pingCtx, cancel := normalizeContext(ctx, cfg.QueryTimeout)
	defer cancel()

	if pingErr := db.PingContext(pingCtx); pingErr != nil {
		_ = db.Close()
		return nil, pingErr
	}

	return &Client{
		db:        db,
		config:    cfg,
		ownsSQLDB: true,
	}, nil
}

// OpenWithDB 包装一个外部已有的 *sql.DB 并按 cfg 设置其连接池参数。
// db 为 nil 时返回 ErrNilDB。db 的所有权仍归调用方，Client.Close 不会关闭它。
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

// DB 返回底层 *sql.DB，供需要直接操作驱动的场景使用。
func (c *Client) DB() *sql.DB {
	return c.db
}

// Config 返回当前配置的副本，修改返回值不影响 Client。
func (c *Client) Config() Config {
	return c.config.Clone()
}

// PingContext 检测数据库连通性，受 QueryTimeout 约束（ctx 已带 deadline 时不叠加）。
func (c *Client) PingContext(ctx context.Context) error {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return c.db.PingContext(queryCtx)
}

// Stats 返回底层连接池的统计信息。
func (c *Client) Stats() sql.DBStats {
	return c.db.Stats()
}

// Close 关闭由 Open 创建的底层 *sql.DB；
// 若 Client 来自 OpenWithDB（外部 db 归调用方所有），则直接返回 nil。
func (c *Client) Close() error {
	if !c.ownsSQLDB || c.db == nil {
		return nil
	}
	return c.db.Close()
}

// ExecContext 在连接池上执行写语句并返回 sql.Result，
// 单条语句受 QueryTimeout 约束（ctx 已带 deadline 时不叠加）。
func (c *Client) ExecContext(ctx context.Context, stmt jetmysql.Statement) (sql.Result, error) {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return stmt.ExecContext(queryCtx, c.db)
}

// QueryContext 在连接池上执行查询并把结果扫描进 dest，
// 单条语句受 QueryTimeout 约束（ctx 已带 deadline 时不叠加）。
func (c *Client) QueryContext(ctx context.Context, stmt jetmysql.Statement, dest any) error {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return stmt.QueryContext(queryCtx, c.db, dest)
}

// Rows 在连接池上执行查询并返回流式结果集，调用方负责关闭返回的 Rows。
// 单条语句受 QueryTimeout 约束（ctx 已带 deadline 时不叠加）。
func (c *Client) Rows(ctx context.Context, stmt jetmysql.Statement) (*jetmysql.Rows, error) {
	queryCtx, cancel := normalizeContext(ctx, c.config.QueryTimeout)
	defer cancel()

	return stmt.Rows(queryCtx, c.db)
}
