// Package db opens the Recall SQLite database (port of src/server/db).
//
// Build with -tags sqlite_fts5: github.com/mattn/go-sqlite3 compiles FTS5 in
// only under that tag, and the search indexes are FTS5 tables. Open refuses a
// build without it.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/zakiego/recall/apps/server/internal/db/sqlitevec" // vec0 on every connection
)

// DB is the database: W is the write pool, capped at one connection so writes
// never interleave (the TS code relied on synchronous better-sqlite3 calls), R
// is a pool for reads. For ":memory:" both are the same single connection.
type DB struct {
	W    *sql.DB
	R    *sql.DB
	Path string
}

// DSN is the go-sqlite3 data source for path with the pragmas every
// connection gets: WAL, synchronous NORMAL, foreign keys on, busy timeout 5 s.
func DSN(path string, txlock string) string {
	q := url.Values{}
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "NORMAL")
	q.Set("_foreign_keys", "1")
	q.Set("_busy_timeout", "5000")
	if txlock != "" {
		q.Set("_txlock", txlock)
	}
	return "file:" + escapePath(path) + "?" + q.Encode()
}

// escapePath keeps a file name with "?", "#" or "%" naming the same file in a
// SQLite URI (better-sqlite3 takes the plain name).
func escapePath(p string) string {
	r := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")
	return r.Replace(p)
}

// OpenRaw opens path (creating its folder) without running migrations.
func OpenRaw(path string) (*DB, error) {
	// "" is a private temporary database in better-sqlite3: one connection,
	// like ":memory:", so every statement sees the same database.
	if path == ":memory:" || path == "" {
		w, err := sql.Open("sqlite3", DSN(path, "immediate"))
		if err != nil {
			return nil, err
		}
		w.SetMaxOpenConns(1)
		w.SetMaxIdleConns(1)
		w.SetConnMaxLifetime(0)
		d := &DB{W: w, R: w, Path: path}
		if err := d.check(); err != nil {
			w.Close()
			return nil, err
		}
		return d, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite3", DSN(path, "immediate"))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxLifetime(0)
	// The read pool refuses writes (query_only), so a write that lands on it
	// by mistake fails instead of being lost.
	r, err := sql.Open("sqlite3", DSN(path, "")+"&_query_only=1")
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	d := &DB{W: w, R: r, Path: path}
	if err := d.check(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) check() error {
	var mode string
	if err := d.W.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("open %s: %w", d.Path, err)
	}
	var fts int
	if err := d.W.QueryRow("SELECT sqlite_compileoption_used('ENABLE_FTS5')").Scan(&fts); err != nil {
		return err
	}
	if fts != 1 {
		return fmt.Errorf("this build has no FTS5: build with -tags sqlite_fts5")
	}
	return nil
}

// Close closes both pools.
func (d *DB) Close() error {
	err := d.W.Close()
	if d.R != d.W {
		if e := d.R.Close(); err == nil {
			err = e
		}
	}
	return err
}

// Open opens path and applies the bundled migrations (openDb in db/index.ts).
func Open(path string) (*DB, error) {
	d, err := OpenRaw(path)
	if err != nil {
		return nil, err
	}
	if err := Migrate(d, Migrations()); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// Memory is a migrated in-memory database (tests).
func Memory() *DB {
	d, err := Open(":memory:")
	if err != nil {
		panic(err)
	}
	return d
}

// VecVersion is SELECT vec_version().
func (d *DB) VecVersion() (string, error) {
	var v string
	err := d.R.QueryRow("SELECT vec_version()").Scan(&v)
	return v, err
}
