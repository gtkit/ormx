package zlogger_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	ormzap "github.com/gtkit/ormx/zlogger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type errorPool struct {
	txPool
	err error
}

func (p errorPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, p.err
}

func TestParameterizedTraceRedactsErrorDetails(t *testing.T) {
	t.Parallel()
	const secret = "private-user@example.test"
	mysqlErr := &mysqldriver.MySQLError{
		Number: 1062, SQLState: [5]byte{'2', '3', '0', '0', '0'},
		Message: "Duplicate entry '" + secret + "' for key 'email'",
	}
	tests := []struct {
		name string
		err  error
	}{
		{name: "mysql", err: mysqlErr},
		{name: "wrapped mysql", err: fmt.Errorf("%s: %w", secret, mysqlErr)},
		{name: "joined mysql", err: errors.Join(errors.New(secret), mysqlErr)},
		{name: "unknown", err: errors.New(secret)},
		{name: "canceled", err: fmt.Errorf("%s: %w", secret, context.Canceled)},
		{name: "deadline", err: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded)},
		{name: "not found", err: fmt.Errorf("%s: %w", secret, gorm.ErrRecordNotFound)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertRedactedQueryError(t, tt.err, secret)
		})
	}
}

func assertRedactedQueryError(t *testing.T, queryErr error, secret string) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		Conn: errorPool{err: queryErr}, SkipInitializeWithVersion: true,
	}), &gorm.Config{DisableAutomaticPing: true, Logger: ormzap.New(ormzap.WithLogger(zap.New(core)))})
	if err != nil {
		t.Fatal(err)
	}
	result := db.WithContext(t.Context()).Exec("INSERT INTO users (email) VALUES (?)", secret)
	if !errors.Is(result.Error, queryErr) {
		t.Fatalf("caller lost original error: %v", result.Error)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("log count=%d, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if strings.Contains(fmt.Sprint(fields), secret) {
		t.Fatalf("sensitive value escaped into structured fields: %v", fields)
	}
	if me, ok := errors.AsType[*mysqldriver.MySQLError](queryErr); ok {
		if fields["mysql_code"] != uint16(1062) || fields["mysql_sqlstate"] != string(me.SQLState[:]) {
			t.Fatalf("missing safe MySQL classification: %v", fields)
		}
	}
}

func TestUnparameterizedTraceKeepsErrorDetails(t *testing.T) {
	t.Parallel()
	core, logs := observer.New(zap.DebugLevel)
	logger := ormzap.New(ormzap.WithLogger(zap.New(core)), ormzap.WithParameterizedQueries(false))
	const detail = "debug-only-error-detail"
	logger.Trace(t.Context(), time.Now(), func() (string, int64) { return "SELECT ?", 0 }, errors.New(detail))
	if logs.Len() != 1 || logs.All()[0].ContextMap()["error"] != detail {
		t.Fatal("explicit debug mode must preserve error detail")
	}
}
