package db

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// Every connection of both pools gets the four pragmas of db/index.ts.
func TestPragmasOnEveryConnection(t *testing.T) {
	d, err := OpenRaw(filepath.Join(t.TempDir(), "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	var conns []*sql.Conn
	for i := 0; i < 4; i++ {
		c, err := d.R.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	w, err := d.W.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	conns = append(conns, w)
	for i, c := range conns {
		var mode string
		var sync, fk, busy int
		if err := c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if mode != "wal" || sync != 1 || fk != 1 || busy != 5000 {
			t.Errorf("connection %d: journal_mode=%s synchronous=%d foreign_keys=%d busy_timeout=%d", i, mode, sync, fk, busy)
		}
	}
	for _, c := range conns {
		c.Close()
	}
}

// A file name with URI characters opens that file, not another one.
func TestOpensPathsWithURICharacters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a?b#c%41.db")
	d, err := OpenRaw(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.W.Exec("CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s: %v", path, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "a?b#c%41.db" && e.Name() != "a?b#c%41.db-wal" && e.Name() != "a?b#c%41.db-shm" {
			t.Errorf("unexpected file %q", e.Name())
		}
	}
}

func TestMemoryDatabaseHasVecAndFTS5(t *testing.T) {
	d, err := OpenRaw(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	v, err := d.VecVersion()
	if err != nil || v != "v0.1.9" {
		t.Fatalf("vec_version %q %v", v, err)
	}
	if _, err := d.W.Exec("CREATE VIRTUAL TABLE f USING fts5(x, tokenize = 'porter unicode61 remove_diacritics 2')"); err != nil {
		t.Fatal(err)
	}
}

// "" is one private temporary database, like better-sqlite3's: a write is seen
// by the next read.
func TestEmptyPathIsOneTemporaryDatabase(t *testing.T) {
	d, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.W.Exec("INSERT INTO memories (id, workspace_id, container, fact) VALUES ('m', 'ws', 'c', 'f')"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.R.QueryRow("SELECT count(*) FROM memories").Scan(&n); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
}

// A write inside ReadTx on a file database fails instead of vanishing.
func TestReadTxRefusesWrites(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	err = d.ReadTx(func(tx Q) error {
		_, err := tx.Exec("INSERT INTO memories (id, workspace_id, container, fact) VALUES ('m', 'ws', 'c', 'f')")
		return err
	})
	if err == nil {
		t.Fatal("write inside ReadTx succeeded")
	}
}
