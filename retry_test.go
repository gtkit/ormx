package ormx

import (
	"errors"
	"fmt"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestIsDeadlock(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "死锁 1213", err: &mysqldriver.MySQLError{Number: 1213}, want: true},
		{name: "锁等待超时 1205", err: &mysqldriver.MySQLError{Number: 1205}, want: true},
		{name: "wrapped 死锁", err: fmt.Errorf("tx: %w", &mysqldriver.MySQLError{Number: 1213}), want: true},
		{name: "其他 MySQL 错误", err: &mysqldriver.MySQLError{Number: 1062}, want: false},
		{name: "非 MySQL 错误", err: errors.New("boom"), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDeadlock(tt.err); got != tt.want {
				t.Fatalf("isDeadlock(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestRetryBackoffWithinJitterRange(t *testing.T) {
	const (
		baseWait = 10 * time.Millisecond
		maxWait  = time.Second
	)
	for attempt := range 4 {
		floor := baseWait << attempt
		ceil := floor + floor/2 // 抖动最多 50%
		for range 50 {
			got := retryBackoff(attempt, baseWait, maxWait)
			if got < floor || got > ceil {
				t.Fatalf("retryBackoff(attempt=%d) = %v, want in [%v, %v]", attempt, got, floor, ceil)
			}
		}
	}
}

func TestRetryBackoffCappedByMaxWait(t *testing.T) {
	const maxWait = 20 * time.Millisecond
	if got := retryBackoff(3, 10*time.Millisecond, maxWait); got != maxWait {
		t.Fatalf("retryBackoff = %v, want capped at %v", got, maxWait)
	}
}

// attempt 极大时左移溢出为负，必须按已达上限处理而不是 panic。
func TestRetryBackoffOverflowReturnsMaxWait(t *testing.T) {
	const maxWait = 50 * time.Millisecond
	for _, attempt := range []int{41, 62, 63} {
		if got := retryBackoff(attempt, 5*time.Millisecond, maxWait); got != maxWait {
			t.Fatalf("retryBackoff(attempt=%d) = %v, want %v", attempt, got, maxWait)
		}
	}
}
