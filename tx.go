package ormx

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/gtkit/ormx/internal/dsn"

	"gorm.io/gorm"
)

const (
	defaultMaxRetries    = 3
	defaultRetryBaseWait = 5 * time.Millisecond
	defaultRetryMaxWait  = 50 * time.Millisecond
)

var errNilTxFunc = errors.New("ormx: nil transaction function")

// TxRetryEvent 描述一次事务死锁重试事件。
type TxRetryEvent struct {
	ClientName string
	Attempt    int
	MaxRetries int
	Wait       time.Duration
	Err        error
}

// TxRetryObserver 在每次事务重试等待前被调用，用于观测重试事件（如记录日志、上报指标）。
type TxRetryObserver func(ctx context.Context, event TxRetryEvent)

// TxOption 配置事务重试行为。
type TxOption func(*txOptions)

type txOptions struct {
	maxRetries    int
	retryBaseWait time.Duration
	retryMaxWait  time.Duration
}

func defaultTxOptions() txOptions {
	return txOptions{
		maxRetries:    defaultMaxRetries,
		retryBaseWait: defaultRetryBaseWait,
		retryMaxWait:  defaultRetryMaxWait,
	}
}

// WithMaxRetries 设置死锁后的最大重试次数。
// 设为 0 表示禁用重试。默认值：3。
func WithMaxRetries(n int) TxOption {
	return func(o *txOptions) {
		if n >= 0 {
			o.maxRetries = n
		}
	}
}

// WithRetryBaseWait 设置指数退避的基础等待时间。
// 默认值：5ms。
func WithRetryBaseWait(d time.Duration) TxOption {
	return func(o *txOptions) {
		if d > 0 {
			o.retryBaseWait = d
		}
	}
}

// WithRetryMaxWait 设置单次重试退避的最大等待时间。
// 默认值：50ms。
func WithRetryMaxWait(d time.Duration) TxOption {
	return func(o *txOptions) {
		if d > 0 {
			o.retryMaxWait = d
		}
	}
}

// WithTx 在事务中执行 fn：fn 返回 nil 则提交，返回 error 则回滚。
// 遇到 MySQL 死锁（1213）或锁等待超时（1205）时按带抖动的指数退避自动重试，
// 重试行为可通过 TxOption 调整；fn 为 nil 时返回错误。
func (c *Client) WithTx(
	ctx context.Context, opts *sql.TxOptions, fn func(tx *gorm.DB) error, txOpts ...TxOption,
) error {
	if fn == nil {
		return errNilTxFunc
	}

	// 入口统一标准化，保证重试等待、observer 回调等全部下游路径拿到非 nil ctx。
	ctx = normalizeContext(ctx)

	// 快路径：未传 TxOption 时直接使用编译期默认值，只在死锁时重试，避免额外分配。
	if len(txOpts) == 0 {
		return c.withTxRetry(ctx, opts, fn, defaultMaxRetries, defaultRetryBaseWait, defaultRetryMaxWait)
	}

	retryOpts := defaultTxOptions()
	for _, opt := range txOpts {
		if opt != nil {
			opt(&retryOpts)
		}
	}
	return c.withTxRetry(ctx, opts, fn, retryOpts.maxRetries, retryOpts.retryBaseWait, retryOpts.retryMaxWait)
}

func (c *Client) withTxRetry(
	ctx context.Context, opts *sql.TxOptions, fn func(tx *gorm.DB) error,
	maxRetries int, baseWait, maxWait time.Duration,
) error {
	var lastErr error
	for attempt := range maxRetries + 1 {
		lastErr = c.execTx(ctx, opts, fn)
		if lastErr == nil {
			return nil
		}
		if !dsn.IsDeadlock(lastErr) {
			return lastErr
		}
		// 检测到死锁后进行带抖动的退避重试，最后一次不再等待。
		if attempt < maxRetries {
			sleep := dsn.RetryBackoff(attempt, baseWait, maxWait)
			if observer := c.config.TxRetryObserver; observer != nil {
				observer(ctx, TxRetryEvent{
					ClientName: c.effectiveName("default"),
					Attempt:    attempt + 1,
					MaxRetries: maxRetries,
					Wait:       sleep,
					Err:        lastErr,
				})
			}
			timer := time.NewTimer(sleep)
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

// WithReadTx 在只读事务中执行 fn，重试行为与 WithTx 的默认值一致。
func (c *Client) WithReadTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return c.WithTx(ctx, &sql.TxOptions{ReadOnly: true}, fn)
}

// execTx 执行单次事务尝试。ctx 已在 WithTx 入口标准化，必定非 nil。
func (c *Client) execTx(ctx context.Context, opts *sql.TxOptions, fn func(tx *gorm.DB) error) (err error) {
	txDB := c.db.WithContext(ctx)
	var tx *gorm.DB
	if opts != nil {
		tx = txDB.Begin(opts)
	} else {
		tx = txDB.Begin()
	}
	if tx.Error != nil {
		return tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback().Error
			panic(r)
		}
	}()

	if err = fn(tx); err != nil {
		return errors.Join(err, rollbackError(tx))
	}

	return tx.Commit().Error
}

func rollbackError(tx *gorm.DB) error {
	if tx == nil {
		return nil
	}
	err := tx.Rollback().Error
	if errors.Is(err, gorm.ErrInvalidTransaction) {
		return nil
	}
	return err
}
