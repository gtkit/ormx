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

// ContextWithWriteFlag marks the context to indicate that a recent write
// has occurred. When passed to [Cluster.ReaderClientCtx] or [Cluster.ReadDBCtx],
// reads will be routed to the primary instead of a replica, ensuring
// read-after-write consistency.
//
// Typical usage: call this right after a successful write, then use the
// returned context for subsequent reads within the same request.
//
//	ctx = orm.ContextWithWriteFlag(ctx)
//	// subsequent reads via ReaderClientCtx(ctx) will hit the primary
func ContextWithWriteFlag(ctx context.Context) context.Context {
	return context.WithValue(normalizeContext(ctx), ctxKeyWriteFlag, writeFlagState{enabled: true})
}

// ContextWithWriteWindow marks the context for a bounded read-after-write window.
// Once ttl elapses, [HasWriteFlag] returns false and reads can return to replicas.
func ContextWithWriteWindow(ctx context.Context, ttl time.Duration) context.Context {
	if ttl <= 0 {
		return ContextClearWriteFlag(ctx)
	}
	return context.WithValue(normalizeContext(ctx), ctxKeyWriteFlag, writeFlagState{
		enabled:   true,
		expiresAt: time.Now().Add(ttl),
	})
}

// ContextClearWriteFlag disables a write flag carried by ctx.
func ContextClearWriteFlag(ctx context.Context) context.Context {
	return context.WithValue(normalizeContext(ctx), ctxKeyWriteFlag, writeFlagState{enabled: false})
}

// HasWriteFlag reports whether the context carries a write flag
// set by [ContextWithWriteFlag].
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
