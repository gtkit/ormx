package jetorm

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	jetmysql "github.com/go-jet/jet/v2/mysql"
)

func TestConfigDefaults(t *testing.T) {
	cfg := NewConfig()

	if cfg.Driver != "mysql" {
		t.Fatalf("expected mysql driver, got %q", cfg.Driver)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("expected default host, got %q", cfg.Host)
	}
	if cfg.Port != "3306" {
		t.Fatalf("expected default port, got %q", cfg.Port)
	}
	if cfg.MaxOpenConns != 50 {
		t.Fatalf("expected default MaxOpenConns 50, got %d", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 10 {
		t.Fatalf("expected default MaxIdleConns 10, got %d", cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime != 30*time.Minute {
		t.Fatalf("expected default ConnMaxLifetime 30m, got %v", cfg.ConnMaxLifetime)
	}
	if cfg.ConnMaxIdleTime != 10*time.Minute {
		t.Fatalf("expected default ConnMaxIdleTime 10m, got %v", cfg.ConnMaxIdleTime)
	}
}

func TestConfigRedactedDSN(t *testing.T) {
	cfg := NewConfig(
		WithUser("alice"),
		WithPassword("secret"),
		WithDatabase("app"),
	)

	redacted := cfg.RedactedDSN()
	if redacted == "" {
		t.Fatal("expected redacted dsn to be non-empty")
	}
	if contains(redacted, "secret") {
		t.Fatalf("expected password to be redacted, got %q", redacted)
	}
	if !contains(redacted, "******") {
		t.Fatalf("expected redaction marker, got %q", redacted)
	}
}

func TestOpenWithDBAppliesPoolOptions(t *testing.T) {
	db := sql.OpenDB(newTestConnector(&testConnBehavior{}))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(
		WithMaxOpenConns(12),
		WithMaxIdleConns(4),
		WithConnMaxLifetime(2*time.Minute),
		WithConnMaxIdleTime(45*time.Second),
	))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	if client.DB() != db {
		t.Fatal("expected wrapped db to be returned")
	}
	stats := db.Stats()
	if stats.MaxOpenConnections != 12 {
		t.Fatalf("expected MaxOpenConnections 12, got %d", stats.MaxOpenConnections)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("expected wrapped db to remain open, got %v", err)
	}
}

func TestOpenPingsAndOwnsSQLDB(t *testing.T) {
	behavior := &testConnBehavior{}
	original := openDBFn
	openDBFn = func(_ Config) (*sql.DB, error) {
		return sql.OpenDB(newTestConnector(behavior)), nil
	}
	t.Cleanup(func() {
		openDBFn = original
	})

	client, err := Open(context.Background(), WithDatabase("app"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if behavior.pingCount != 1 {
		t.Fatalf("expected ping during Open, got %d", behavior.pingCount)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.DB().PingContext(context.Background()); err == nil {
		t.Fatal("expected owned db to be closed")
	}
}

func TestClientExecContextUsesConfiguredTimeoutWhenMissingDeadline(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithQueryTimeout(250*time.Millisecond)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	if _, err := client.ExecContext(nil, jetmysql.RawStatement("UPDATE demo SET n = 1")); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	if !behavior.execSawDeadline {
		t.Fatal("expected ExecContext to apply query timeout")
	}
}

func TestClientQueryContextUsesProvidedContextDeadline(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithQueryTimeout(2*time.Second)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	var dest []struct {
		ID int64
	}
	err = client.QueryContext(ctx, jetmysql.RawStatement("SELECT 1 AS id"), &dest)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	if !behavior.querySawDeadline {
		t.Fatal("expected query to observe caller deadline")
	}
}

func TestClientRowsReturnsJetRows(t *testing.T) {
	db := sql.OpenDB(newTestConnector(&testConnBehavior{}))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	rows, err := client.Rows(context.Background(), jetmysql.RawStatement("SELECT 1 AS id"))
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected one row")
	}

	var dest struct {
		ID int64
	}
	if err := rows.Scan(&dest); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if dest.ID != 1 {
		t.Fatalf("expected scanned row id 1, got %d", dest.ID)
	}
}

func TestWithTxCommitsOnSuccess(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		_, err := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}
	if behavior.beginCount != 1 {
		t.Fatalf("expected one tx begin, got %d", behavior.beginCount)
	}
	if behavior.commitCount != 1 {
		t.Fatalf("expected commit count 1, got %d", behavior.commitCount)
	}
	if behavior.rollbackCount != 0 {
		t.Fatalf("expected rollback count 0, got %d", behavior.rollbackCount)
	}
}

func TestWithTxRollsBackOnError(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	sentinel := errors.New("boom")
	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		_, execErr := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		if execErr != nil {
			t.Fatalf("ExecContext: %v", execErr)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if behavior.commitCount != 0 {
		t.Fatalf("expected commit count 0, got %d", behavior.commitCount)
	}
	if behavior.rollbackCount != 1 {
		t.Fatalf("expected rollback count 1, got %d", behavior.rollbackCount)
	}
}

func TestWithTxRollsBackOnPanic(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	defer func() {
		recovered := recover()
		if recovered != "panic-value" {
			t.Fatalf("expected panic-value, got %#v", recovered)
		}
		if behavior.rollbackCount != 1 {
			t.Fatalf("expected rollback count 1, got %d", behavior.rollbackCount)
		}
	}()

	_ = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		_, err := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		if err != nil {
			t.Fatalf("ExecContext: %v", err)
		}
		panic("panic-value")
	})
}

func TestWithTxRejectsNilCallback(t *testing.T) {
	db := sql.OpenDB(newTestConnector(&testConnBehavior{}))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, nil)
	if !errors.Is(err, ErrNilTxFunc) {
		t.Fatalf("expected ErrNilTxFunc, got %v", err)
	}
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && func() bool {
		return stringContains(s, substr)
	}()
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

type testConnBehavior struct {
	pingCount        int
	beginCount       int
	commitCount      int
	rollbackCount    int
	execCount        int
	execSawDeadline  bool
	querySawDeadline bool
	// execErrQueue 中的错误按调用顺序依次返回，耗尽后恢复成功。
	execErrQueue []error
}

type testConnector struct {
	behavior *testConnBehavior
}

func newTestConnector(behavior *testConnBehavior) *testConnector {
	return &testConnector{behavior: behavior}
}

func (c *testConnector) Connect(context.Context) (driver.Conn, error) {
	return &testConn{behavior: c.behavior}, nil
}

func (c *testConnector) Driver() driver.Driver {
	return testDriver{behavior: c.behavior}
}

type testDriver struct {
	behavior *testConnBehavior
}

func (d testDriver) Open(string) (driver.Conn, error) {
	return &testConn{behavior: d.behavior}, nil
}

type testConn struct {
	behavior *testConnBehavior
}

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return testStmt{}, nil
}

func (c *testConn) Close() error {
	return nil
}

func (c *testConn) Begin() (driver.Tx, error) {
	c.behavior.beginCount++
	return &testTx{behavior: c.behavior}, nil
}

func (c *testConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.behavior.beginCount++
	return &testTx{behavior: c.behavior}, nil
}

func (c *testConn) Ping(context.Context) error {
	c.behavior.pingCount++
	return nil
}

func (c *testConn) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	_, c.behavior.execSawDeadline = ctx.Deadline()
	c.behavior.execCount++
	if len(c.behavior.execErrQueue) > 0 {
		err := c.behavior.execErrQueue[0]
		c.behavior.execErrQueue = c.behavior.execErrQueue[1:]
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (c *testConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	_, c.behavior.querySawDeadline = ctx.Deadline()
	return &testRows{
		columns: []string{"id"},
		values:  [][]driver.Value{{int64(1)}},
	}, nil
}

type testStmt struct{}

func (testStmt) Close() error {
	return nil
}

func (testStmt) NumInput() int {
	return -1
}

func (testStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}

func (testStmt) Query([]driver.Value) (driver.Rows, error) {
	return &testRows{
		columns: []string{"id"},
		values:  [][]driver.Value{{int64(1)}},
	}, nil
}

type testTx struct {
	behavior *testConnBehavior
}

func (t *testTx) Commit() error {
	t.behavior.commitCount++
	return nil
}

func (t *testTx) Rollback() error {
	t.behavior.rollbackCount++
	return nil
}

type testRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *testRows) Columns() []string {
	return r.columns
}

func (r *testRows) Close() error {
	return nil
}

func (r *testRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
