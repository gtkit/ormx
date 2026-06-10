package ormx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync/atomic"
)

type stubDBState struct {
	pingErr       error
	pingErrOnce   error
	pingHook      func()
	beginErr      error
	commitErr     error
	commitErrOnce error // returned once on first commit, then cleared
	rollbackErr   error
	pingCount     atomic.Int32
	beginCount    atomic.Int32
	readOnlyCount atomic.Int32
	commitCount   atomic.Int32
	rollbackCount atomic.Int32
}

type stubDBOption func(*stubDBState)

func withStubPingError(err error) stubDBOption {
	return func(state *stubDBState) {
		state.pingErr = err
	}
}

func withStubPingErrorOnce(err error) stubDBOption {
	return func(state *stubDBState) {
		state.pingErrOnce = err
	}
}

func withStubPingHook(hook func()) stubDBOption {
	return func(state *stubDBState) {
		state.pingHook = hook
	}
}

func newStubDB(opts ...stubDBOption) (*sql.DB, *stubDBState) {
	state := &stubDBState{}
	for _, opt := range opts {
		opt(state)
	}
	return sql.OpenDB(&stubConnector{state: state}), state
}

type stubConnector struct {
	state *stubDBState
}

func (c *stubConnector) Connect(context.Context) (driver.Conn, error) {
	return &stubConn{state: c.state}, nil
}

func (c *stubConnector) Driver() driver.Driver {
	return stubDriver{state: c.state}
}

type stubDriver struct {
	state *stubDBState
}

func (d stubDriver) Open(string) (driver.Conn, error) {
	return &stubConn{state: d.state}, nil
}

type stubConn struct {
	state *stubDBState
}

func (c *stubConn) Prepare(string) (driver.Stmt, error) {
	return stubStmt{}, nil
}

func (c *stubConn) Close() error {
	return nil
}

func (c *stubConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *stubConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.state != nil {
		c.state.beginCount.Add(1)
		if opts.ReadOnly {
			c.state.readOnlyCount.Add(1)
		}
		if c.state.beginErr != nil {
			return nil, c.state.beginErr
		}
	}
	return &stubTx{state: c.state}, nil
}

func (c *stubConn) Ping(context.Context) error {
	if c.state != nil {
		c.state.pingCount.Add(1)
		if c.state.pingHook != nil {
			c.state.pingHook()
		}
	}
	if c.state != nil && c.state.pingErr != nil {
		return c.state.pingErr
	}
	if c.state != nil && c.state.pingErrOnce != nil {
		err := c.state.pingErrOnce
		c.state.pingErrOnce = nil
		return err
	}
	return nil
}

type stubStmt struct{}

func (stubStmt) Close() error {
	return nil
}

func (stubStmt) NumInput() int {
	return 0
}

func (stubStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (stubStmt) Query([]driver.Value) (driver.Rows, error) {
	return stubRows{}, nil
}

type stubTx struct {
	state *stubDBState
}

func (t *stubTx) Commit() error {
	if t.state != nil {
		t.state.commitCount.Add(1)
		if t.state.commitErrOnce != nil {
			err := t.state.commitErrOnce
			t.state.commitErrOnce = nil
			return err
		}
		if t.state.commitErr != nil {
			return t.state.commitErr
		}
	}
	return nil
}

func (t *stubTx) Rollback() error {
	if t.state != nil {
		t.state.rollbackCount.Add(1)
		if t.state.rollbackErr != nil {
			return t.state.rollbackErr
		}
	}
	return nil
}

type stubRows struct{}

func (stubRows) Columns() []string {
	return nil
}

func (stubRows) Close() error {
	return nil
}

func (stubRows) Next([]driver.Value) error {
	return driver.ErrBadConn
}
