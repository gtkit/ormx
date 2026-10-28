package ormx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"
	"testing/synctest"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type initializationConnector struct {
	state *stubDBState
	query func(context.Context) (driver.Rows, error)
}

func (c *initializationConnector) Connect(context.Context) (driver.Conn, error) {
	return &initializationConn{stubConn: stubConn{state: c.state}, query: c.query}, nil
}

func (c *initializationConnector) Driver() driver.Driver { return stubDriver{state: c.state} }

type initializationConn struct {
	stubConn
	query func(context.Context) (driver.Rows, error)
}

func (c *initializationConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	return c.query(ctx)
}

type versionRows struct {
	version string
	done    bool
}

func (*versionRows) Columns() []string { return []string{"VERSION()"} }
func (*versionRows) Close() error      { return nil }
func (r *versionRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	dest[0] = r.version
	r.done = true
	return nil
}

func newInitializationDB(t *testing.T, query func(context.Context) (driver.Rows, error)) (*sql.DB, *stubDBState) {
	t.Helper()
	state := &stubDBState{}
	db := sql.OpenDB(&initializationConnector{state: state, query: query})
	t.Cleanup(func() { _ = db.Close() })
	return db, state
}

func TestOpenWithDBVersionFailureKeepsPool(t *testing.T) {
	t.Parallel()
	want := errors.New("version probe failed")
	db, _ := newInitializationDB(t, func(context.Context) (driver.Rows, error) { return nil, want })
	_, err := OpenWithDB(t.Context(), db)
	if !errors.Is(err, want) {
		t.Fatalf("version error not preserved: %v", err)
	}
	if pingErr := db.PingContext(t.Context()); pingErr != nil {
		t.Fatalf("borrowed pool was closed: %v", pingErr)
	}
}

func TestOpenWithDBVersionUsesCallerContext(t *testing.T) {
	t.Parallel()
	type contextKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "startup"))
	defer cancel()
	db, _ := newInitializationDB(t, func(queryCtx context.Context) (driver.Rows, error) {
		if queryCtx.Value(contextKey{}) != "startup" {
			return nil, errors.New("missing startup context")
		}
		cancel()
		return nil, queryCtx.Err()
	})
	_, err := OpenWithDB(ctx, db, WithStartupPing(false))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation not propagated: %v", err)
	}
	if pingErr := db.PingContext(t.Context()); pingErr != nil {
		t.Fatalf("borrowed pool unusable after cancellation: %v", pingErr)
	}
}

func TestOpenWithDBVersionHonorsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		db, _ := newInitializationDB(t, func(queryCtx context.Context) (driver.Rows, error) {
			select {
			case <-queryCtx.Done():
				return nil, queryCtx.Err()
			case <-time.After(2 * time.Second):
				return nil, errors.New("query missed caller deadline")
			}
		})
		_, err := OpenWithDB(ctx, db, WithStartupPing(false))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("caller deadline not propagated: %v", err)
		}
	})
}

func TestOpenWithDBPoolWaitHonorsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, _ := newInitializationDB(t, func(context.Context) (driver.Rows, error) {
			return &versionRows{version: "8.0.22"}, nil
		})
		db.SetMaxOpenConns(1)
		held, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, openErr := OpenWithDB(ctx, db, WithStartupPing(false))
			done <- openErr
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case openErr := <-done:
			if !errors.Is(openErr, context.Canceled) {
				t.Fatalf("pool wait error=%v", openErr)
			}
		default:
			_ = held.Close()
			openErr := <-done
			t.Fatalf("version probe kept waiting for a pool connection after cancellation: %v", openErr)
		}
	})
}

func TestOpenWithDBInitializationRestoresPools(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, version := range []string{"5.7.44", "8.0.22"} {
			t.Run(version+"/prepare="+fmt.Sprint(prepare), func(t *testing.T) {
				db, state := newInitializationDB(t, func(context.Context) (driver.Rows, error) {
					return &versionRows{version: version}, nil
				})
				ctx, cancel := context.WithCancel(t.Context())
				client, err := OpenWithDB(ctx, db, WithPrepareStmt(prepare), WithTranslateError(true))
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				assertInitializedClient(t, client, db, state, version)
			})
		}
	}
}

func assertInitializedClient(t *testing.T, client *Client, db *sql.DB, state *stubDBState, version string) {
	t.Helper()
	gormDB := client.DB()
	underlying, err := gormDB.DB()
	if err != nil || underlying != db || client.SQLDB() != db {
		t.Fatalf("original pool not restored: %v", err)
	}
	dialect, ok := gormDB.Dialector.(*gormmysql.Dialector)
	if !ok || dialect.Conn != db || dialect.DontSupportRenameColumn != (version == "5.7.44") {
		t.Fatal("MySQL version capabilities or original dialect pool were lost")
	}
	if txErr := client.Transaction(t.Context(), func(tx *gorm.DB) error { return tx.Exec("SELECT 1").Error }); txErr != nil {
		t.Fatalf("runtime transaction after startup cancellation: %v", txErr)
	}
	if state.commitCount.Load() != 1 {
		t.Fatal("transaction did not commit")
	}
	translated := gormDB.Session(&gorm.Session{})
	if translatedErr := translated.AddError(&mysqldriver.MySQLError{Number: 1062}); !errors.Is(translatedErr, gorm.ErrDuplicatedKey) {
		t.Fatalf("error translation lost: %v", translatedErr)
	}
	if closeErr := client.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if pingErr := db.PingContext(t.Context()); pingErr != nil {
		t.Fatalf("Close closed borrowed pool: %v", pingErr)
	}
}
