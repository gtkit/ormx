package jetorm

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	jetmysql "github.com/go-jet/jet/v2/mysql"
)

// QueryTimeout 只约束单条语句，不再限制整个事务的生命周期。
func TestWithTxNotLimitedByQueryTimeout(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithQueryTimeout(30*time.Millisecond)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		time.Sleep(90 * time.Millisecond) // 总时长超过 QueryTimeout
		_, execErr := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		return execErr
	})
	if err != nil {
		t.Fatalf("expected commit despite exceeding QueryTimeout, got %v", err)
	}
	if behavior.commitCount != 1 {
		t.Fatalf("expected commit count 1, got %d", behavior.commitCount)
	}
}

// TxTimeout 限制事务总时长，超时后事务被回滚。
func TestWithTxTimeoutRollsBackLongTx(t *testing.T) {
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithTxTimeout(40*time.Millisecond)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(_ *Tx) error {
		time.Sleep(150 * time.Millisecond)
		return nil
	})
	if err == nil {
		t.Fatal("expected error after exceeding TxTimeout")
	}
	if behavior.commitCount != 0 {
		t.Fatalf("expected commit count 0, got %d", behavior.commitCount)
	}
}

// 默认不重试：死锁错误直接返回，事务只执行一次。
func TestWithTxNoRetryByDefault(t *testing.T) {
	behavior := &testConnBehavior{
		execErrQueue: []error{&mysqldriver.MySQLError{Number: 1213, Message: "Deadlock found"}},
	}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		_, execErr := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		return execErr
	})
	var mysqlErr *mysqldriver.MySQLError
	if !errors.As(err, &mysqlErr) || mysqlErr.Number != 1213 {
		t.Fatalf("expected deadlock error, got %v", err)
	}
	if behavior.beginCount != 1 {
		t.Fatalf("expected single attempt, got %d begins", behavior.beginCount)
	}
}

// 显式开启重试：首次死锁、第二次成功。
func TestWithTxRetriesDeadlock(t *testing.T) {
	behavior := &testConnBehavior{
		execErrQueue: []error{&mysqldriver.MySQLError{Number: 1213, Message: "Deadlock found"}},
	}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithTxRetry(2, 0, 0)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	attempts := 0
	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		attempts++
		_, execErr := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		return execErr
	})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected tx func executed twice, got %d", attempts)
	}
	if behavior.commitCount != 1 {
		t.Fatalf("expected commit count 1, got %d", behavior.commitCount)
	}
	if behavior.rollbackCount != 1 {
		t.Fatalf("expected rollback count 1, got %d", behavior.rollbackCount)
	}
}

// 非死锁错误不触发重试。
func TestWithTxRetrySkipsNonDeadlockError(t *testing.T) {
	sentinel := errors.New("boom")
	behavior := &testConnBehavior{execErrQueue: []error{sentinel}}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithTxRetry(3, 0, 0)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		_, execErr := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		return execErr
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if behavior.beginCount != 1 {
		t.Fatalf("expected single attempt, got %d begins", behavior.beginCount)
	}
}

// fn 出错且回滚失败时，两个错误都必须传播给调用方。
func TestWithTxJoinsRollbackError(t *testing.T) {
	fnErr := errors.New("fn failed")
	rollbackErr := errors.New("rollback failed")
	behavior := &testConnBehavior{rollbackErr: rollbackErr}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig())
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(_ *Tx) error {
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Fatalf("expected fn error preserved, got %v", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback error joined, got %v", err)
	}
}

// 回滚返回 sql.ErrTxDone（事务已被终止）属正常竞态，不应污染返回错误。
func TestWithTxIgnoresTxDoneOnRollback(t *testing.T) {
	fnErr := errors.New("fn failed")
	behavior := &testConnBehavior{}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithTxTimeout(40*time.Millisecond)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	err = client.WithTx(context.Background(), nil, func(_ *Tx) error {
		time.Sleep(150 * time.Millisecond) // 等待 TxTimeout 触发驱动层自动回滚
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Fatalf("expected fn error preserved, got %v", err)
	}
	if errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("expected sql.ErrTxDone to be filtered, got %v", err)
	}
}

// 回滚失败被 Join 后，死锁判定与重试不受影响。
func TestWithTxRetriesDeadlockDespiteRollbackError(t *testing.T) {
	behavior := &testConnBehavior{
		execErrQueue: []error{&mysqldriver.MySQLError{Number: 1213, Message: "Deadlock found"}},
		rollbackErr:  errors.New("rollback failed"),
	}
	db := sql.OpenDB(newTestConnector(behavior))
	t.Cleanup(func() { _ = db.Close() })

	client, err := OpenWithDB(db, NewConfig(WithTxRetry(2, 0, 0)))
	if err != nil {
		t.Fatalf("OpenWithDB: %v", err)
	}

	attempts := 0
	err = client.WithTx(context.Background(), nil, func(tx *Tx) error {
		attempts++
		_, execErr := tx.ExecContext(context.Background(), jetmysql.RawStatement("UPDATE demo SET n = 1"))
		return execErr
	})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected tx func executed twice, got %d", attempts)
	}
}

func TestWithDSNParamAndTimeoutOverrides(t *testing.T) {
	cfg := NewConfig(
		WithDatabase("app"),
		WithDSNParam("charset", "utf8mb4"),
		WithReadTimeout(5*time.Second),
		WithLoc(time.UTC),
	)

	dsnStr := cfg.DSN()
	if !strings.Contains(dsnStr, "charset=utf8mb4") {
		t.Fatalf("expected charset param in dsn, got %q", dsnStr)
	}
	if !strings.Contains(dsnStr, "readTimeout=5s") {
		t.Fatalf("expected readTimeout=5s in dsn, got %q", dsnStr)
	}
	if strings.Contains(dsnStr, "loc=Local") {
		t.Fatalf("expected loc to be UTC (driver default, omitted), got %q", dsnStr)
	}
}

func TestWithDSNParamIgnoresEmptyKey(t *testing.T) {
	cfg := NewConfig(WithDSNParam("", "x"))
	if len(cfg.Params) != 0 {
		t.Fatalf("expected empty key to be ignored, got %v", cfg.Params)
	}
}
