package db

import (
	"database/sql"
	"fmt"
	"sync/atomic"
)

// Q runs statements on the database or inside a transaction.
//
// The TS code ran every statement on one synchronous better-sqlite3
// connection, so a read followed by a write without an await in between could
// not interleave with another request. Here requests run concurrently: put
// every read-then-write, every claim and every multi-statement change in one
// Tx, and pass the tx down instead of the *DB. Inside a Tx callback use only
// the tx (the write pool has one connection, so calling the *DB deadlocks), and
// close Rows before the next statement.
type Q interface {
	// Exec runs a write on the write connection (or in the transaction).
	Exec(query string, args ...any) (sql.Result, error)
	// Query and QueryRow read from the read pool (or in the transaction).
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	// WQuery and WQueryRow run on the write connection: INSERT/UPDATE ...
	// RETURNING, and reads that must see the writer's latest state.
	WQuery(query string, args ...any) (*sql.Rows, error)
	WQueryRow(query string, args ...any) *sql.Row
	// Tx runs fn in a transaction: BEGIN IMMEDIATE on the *DB, a savepoint
	// inside a transaction (better-sqlite3 nests transactions the same way).
	// An error from fn rolls back and is returned.
	Tx(fn func(tx Q) error) error
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) { return d.W.Exec(query, args...) }
func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.R.Query(query, args...)
}
func (d *DB) QueryRow(query string, args ...any) *sql.Row { return d.R.QueryRow(query, args...) }
func (d *DB) WQuery(query string, args ...any) (*sql.Rows, error) {
	return d.W.Query(query, args...)
}
func (d *DB) WQueryRow(query string, args ...any) *sql.Row { return d.W.QueryRow(query, args...) }

// Tx runs fn in one write transaction.
func (d *DB) Tx(fn func(tx Q) error) (err error) {
	tx, err := d.W.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
		if r := recover(); r != nil {
			panic(r)
		}
	}()
	if err := fn(&Tx{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ReadTx runs fn in one read transaction on the read pool, so several reads
// see one snapshot (the TS build read everything in one synchronous run).
// Writes inside it fail (the read pool is query_only). On a ":memory:"
// database it is a write transaction.
func (d *DB) ReadTx(fn func(tx Q) error) error {
	if d.R == d.W {
		return d.Tx(fn)
	}
	tx, err := d.R.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(&Tx{tx: tx})
}

// ReadTx on a Q: a *DB opens a read transaction, a transaction is used as it is.
func ReadTx(q Q, fn func(tx Q) error) error {
	if d, ok := q.(*DB); ok {
		return d.ReadTx(fn)
	}
	return fn(q)
}

// Tx is a Q inside a transaction.
type Tx struct{ tx *sql.Tx }

var savepointSeq atomic.Int64

func (t *Tx) Exec(query string, args ...any) (sql.Result, error) { return t.tx.Exec(query, args...) }
func (t *Tx) Query(query string, args ...any) (*sql.Rows, error) {
	return t.tx.Query(query, args...)
}
func (t *Tx) QueryRow(query string, args ...any) *sql.Row { return t.tx.QueryRow(query, args...) }
func (t *Tx) WQuery(query string, args ...any) (*sql.Rows, error) {
	return t.tx.Query(query, args...)
}
func (t *Tx) WQueryRow(query string, args ...any) *sql.Row { return t.tx.QueryRow(query, args...) }

// Tx inside a transaction is a savepoint: an error rolls back to it and leaves
// the outer transaction open.
func (t *Tx) Tx(fn func(tx Q) error) error {
	name := fmt.Sprintf("sp_%d", savepointSeq.Add(1))
	if _, err := t.tx.Exec("SAVEPOINT " + name); err != nil {
		return err
	}
	if err := fn(t); err != nil {
		_, _ = t.tx.Exec("ROLLBACK TO " + name)
		_, _ = t.tx.Exec("RELEASE " + name)
		return err
	}
	_, err := t.tx.Exec("RELEASE " + name)
	return err
}

// Changes returns the rows a statement changed (better-sqlite3's .changes).
func Changes(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
