package zlogger_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/gtkit/ormx/paginator"
	ormzap "github.com/gtkit/ormx/zlogger"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var errDryRun = errors.New("dry run: connection must not be used")

// txPool 是只能开事务的连接池替身：配合 DryRun，GORM 走完真实调用链并输出 Trace，但不访问连接。
type txPool struct{}

func (txPool) PrepareContext(context.Context, string) (*sql.Stmt, error) { return nil, errDryRun }

func (txPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errDryRun
}

func (txPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errDryRun
}

func (txPool) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func (txPool) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) { return &txConn{}, nil }

// txConn 必须是指针：GORM 回滚前对 TxCommitter 调 reflect.Value.IsNil。
type txConn struct{ txPool }

func (*txConn) Commit() error   { return nil }
func (*txConn) Rollback() error { return nil }

type widget struct {
	ID   int64
	Name string
}

func dryRunDB(t *testing.T) (*gorm.DB, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.DebugLevel)
	db, err := gorm.Open(
		gormmysql.New(gormmysql.Config{Conn: txPool{}, SkipInitializeWithVersion: true}),
		&gorm.Config{
			DryRun: true,
			Logger: ormzap.New(ormzap.WithLogger(zap.New(core)), ormzap.WithLogLevel(gormlogger.Info)),
		},
	)
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	return db.WithContext(t.Context()), logs
}

// assertSource 要求至少一条 SQL 以 sqlPrefix 开头的日志，且所有这类日志的 source 都等于 want。
func assertSource(t *testing.T, logs *observer.ObservedLogs, sqlPrefix, want string) {
	t.Helper()
	matched := 0
	for _, entry := range logs.All() {
		fields := entry.ContextMap()
		if stmt, _ := fields["sql"].(string); !strings.HasPrefix(stmt, sqlPrefix) {
			continue
		}
		matched++
		if got := fields["source"]; got != want {
			t.Errorf("sql %q source = %v, want %s", fields["sql"], got, want)
		}
	}
	if matched == 0 {
		t.Fatalf("no log entry with sql prefix %q in %d entries", sqlPrefix, logs.Len())
	}
}

// ormx 包装层（分页器）内部发出的 SQL，source 必须越过包装层指向业务调用行；
// 删掉 ormx 前缀判定时 source 会停在 paginator.go，本测试即失败。
func TestSourceSkipsOrmxWrapper(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		opts      []paginator.Option
		sqlPrefix string
	}{
		{name: "count query", sqlPrefix: "SELECT count(*)"},
		{name: "page query", opts: []paginator.Option{paginator.WithTotal(5)}, sqlPrefix: "SELECT * FROM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db, logs := dryRunDB(t)

			_, file, line, _ := runtime.Caller(0)
			_, err := paginator.Paginate[widget](db, paginator.Params{Page: 1, PageSize: 10}, tt.opts...)
			if err != nil {
				t.Fatalf("Paginate() error = %v", err)
			}
			assertSource(t, logs, tt.sqlPrefix, fmt.Sprintf("%s:%d", file, line+1))
		})
	}
}

// 嵌套事务的 ROLLBACK TO SAVEPOINT 在 panic 展开中执行，栈上夹着 runtime 帧；
// source 必须越过 runtime 指向业务的 panic 行，删掉 runtime 前缀判定时会停在 runtime/panic.go。
func TestSourceDuringPanicRollbackPointsToPanicSite(t *testing.T) {
	t.Parallel()
	db, logs := dryRunDB(t)

	var (
		file string
		line int
	)
	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recover() = %v, want boom", r)
			}
		}()
		_ = db.Transaction(func(tx *gorm.DB) error {
			return tx.Transaction(func(*gorm.DB) error {
				_, file, line, _ = runtime.Caller(0)
				panic("boom")
			})
		})
	}()
	assertSource(t, logs, "ROLLBACK TO SAVEPOINT", fmt.Sprintf("%s:%d", file, line+1))
}
