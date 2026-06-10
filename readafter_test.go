package ormx

import (
	"context"
	"testing"
	"time"
)

func TestContextWithWriteFlagAcceptsNilContext(t *testing.T) {
	var nilCtx context.Context

	ctx := ContextWithWriteFlag(nilCtx)
	if !HasWriteFlag(ctx) {
		t.Fatal("expected write flag on returned context")
	}
}

func TestHasWriteFlagReturnsFalseForNilContext(t *testing.T) {
	var nilCtx context.Context

	if HasWriteFlag(nilCtx) {
		t.Fatal("expected false for nil context")
	}
}

func TestContextClearWriteFlag(t *testing.T) {
	ctx := ContextWithWriteFlag(context.Background())
	if !HasWriteFlag(ctx) {
		t.Fatal("expected true after set")
	}

	ctx = ContextClearWriteFlag(ctx)
	if HasWriteFlag(ctx) {
		t.Fatal("expected false after clear")
	}
}

func TestContextWithWriteWindow(t *testing.T) {
	ctx := ContextWithWriteWindow(context.Background(), 20*time.Millisecond)
	if !HasWriteFlag(ctx) {
		t.Fatal("expected true before window expires")
	}

	time.Sleep(40 * time.Millisecond)
	if HasWriteFlag(ctx) {
		t.Fatal("expected false after window expires")
	}
}
