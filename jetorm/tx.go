package jetorm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gtkit/ormx/internal/dsn"

	jetmysql "github.com/go-jet/jet/v2/mysql"
)

const (
	defaultTxRetryBaseWait = 5 * time.Millisecond
	defaultTxRetryMaxWait  = 50 * time.Millisecond
)

type Tx struct {
	tx     *sql.Tx
	config Config
}

// WithTx 在事务中执行 fn：fn 返回 nil 则提交，否则回滚；panic 时回滚后继续抛出。
// 配置了 TxMaxRetries 时，死锁（1213）/锁等待超时（1205）会按退避自动重试，
// fn 可能被执行多次，必须保证幂等。
func (c *Client) WithTx(ctx context.Context, opts *sql.TxOptions, fn func(*Tx) error) error {
	if fn == nil {
		return ErrNilTxFunc
	}

	ctx = ensureContext(ctx)

	maxRetries := max(c.config.TxMaxRetries, 0)
	baseWait := c.config.TxRetryBaseWait
	if baseWait <= 0 {
		baseWait = defaultTxRetryBaseWait
	}
	maxWait := c.config.TxRetryMaxWait
	if maxWait <= 0 {
		maxWait = defaultTxRetryMaxWait
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = c.execTx(ctx, opts, fn)
		if lastErr == nil {
			return nil
		}
		if !dsn.IsDeadlock(lastErr) {
			return lastErr
		}
		if attempt < maxRetries {
			timer := time.NewTimer(dsn.RetryBackoff(attempt, baseWait, maxWait))
			select {
			case <-ctx.Done():
				timer.Stop()
				return errors.Join(lastErr, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return lastErr
}

// execTx 执行单次事务尝试。事务生命周期仅受 TxTimeout（若设置）与调用方
// context 限制，QueryTimeout 不参与，避免误杀长事务。
func (c *Client) execTx(ctx context.Context, opts *sql.TxOptions, fn func(*Tx) error) (err error) {
	txCtx := ctx
	cancel := context.CancelFunc(func() {})
	if c.config.TxTimeout > 0 {
		txCtx, cancel = context.WithTimeout(ctx, c.config.TxTimeout)
	}
	defer cancel()

	sqlTx, err := c.db.BeginTx(txCtx, opts)
	if err != nil {
		return err
	}

	tx := &Tx{tx: sqlTx, config: c.config.Clone()}

	defer func() {
		if recovered := recover(); recovered != nil {
			_ = sqlTx.Rollback()
			panic(recovered)
		}
		if err != nil {
			_ = sqlTx.Rollback()
			return
		}
		err = sqlTx.Commit()
	}()

	err = fn(tx)
	return err
}

func (t *Tx) ExecContext(ctx context.Context, stmt jetmysql.Statement) (sql.Result, error) {
	queryCtx, cancel := normalizeContext(ctx, t.config.QueryTimeout)
	defer cancel()

	return stmt.ExecContext(queryCtx, t.tx)
}

func (t *Tx) QueryContext(ctx context.Context, stmt jetmysql.Statement, dest any) error {
	queryCtx, cancel := normalizeContext(ctx, t.config.QueryTimeout)
	defer cancel()

	return stmt.QueryContext(queryCtx, t.tx, dest)
}

func (t *Tx) Rows(ctx context.Context, stmt jetmysql.Statement) (*jetmysql.Rows, error) {
	queryCtx, cancel := normalizeContext(ctx, t.config.QueryTimeout)
	defer cancel()

	return stmt.Rows(queryCtx, t.tx)
}
