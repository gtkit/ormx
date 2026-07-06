package ormx

import (
	"context"
	"time"
)

type ctxKey int

const ctxKeyWriteFlag ctxKey = iota

type writeFlagState struct {
	enabled   bool
	expiresAt time.Time
}

// ContextWithWriteFlag 在 ctx 上打写标记，表示刚发生过一次写入。
// 带该标记的 ctx 传给 [Cluster.ReaderClientCtx] 或 [Cluster.ReadDBCtx] 时，
// 读请求会被路由到主库而非副本，保证写后读一致性。
//
// 典型用法：写入成功后立即调用，本次请求内的后续读操作复用返回的 ctx。
//
//	ctx = ormx.ContextWithWriteFlag(ctx)
//	// 之后经 ReaderClientCtx(ctx) 的读请求会命中主库
func ContextWithWriteFlag(ctx context.Context) context.Context {
	return context.WithValue(normalizeContext(ctx), ctxKeyWriteFlag, writeFlagState{enabled: true})
}

// ContextWithWriteWindow 在 ctx 上打带时限的写标记：ttl 内读请求路由主库，
// ttl 过后 [HasWriteFlag] 返回 false，读请求自动恢复走副本；
// ttl ≤ 0 等价于清除写标记。
func ContextWithWriteWindow(ctx context.Context, ttl time.Duration) context.Context {
	if ttl <= 0 {
		return ContextClearWriteFlag(ctx)
	}
	return context.WithValue(normalizeContext(ctx), ctxKeyWriteFlag, writeFlagState{
		enabled:   true,
		expiresAt: time.Now().Add(ttl),
	})
}

// ContextClearWriteFlag 清除 ctx 携带的写标记。
func ContextClearWriteFlag(ctx context.Context) context.Context {
	return context.WithValue(normalizeContext(ctx), ctxKeyWriteFlag, writeFlagState{enabled: false})
}

// HasWriteFlag 报告 ctx 是否携带仍然有效的写标记
// （由 [ContextWithWriteFlag] 或 [ContextWithWriteWindow] 设置）。
func HasWriteFlag(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	switch v := ctx.Value(ctxKeyWriteFlag).(type) {
	case writeFlagState:
		if !v.enabled {
			return false
		}
		return v.expiresAt.IsZero() || time.Now().Before(v.expiresAt)
	default:
		return false
	}
}
