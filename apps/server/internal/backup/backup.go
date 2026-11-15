// Package backup is backup.ts: a consistent, gzipped snapshot of the live
// database through SQLite's online backup API, safe while the app writes.
package backup

import (
	"compress/gzip"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// Result is BackupResult.
type Result struct {
	Dest    string `json:"dest"`
	Bytes   int64  `json:"bytes"`
	DBBytes int64  `json:"dbBytes"`
}

func open(dsn string) (*sqlite3.SQLiteConn, error) {
	c, err := (&sqlite3.SQLiteDriver{}).Open(dsn)
	if err != nil {
		return nil, err
	}
	return c.(*sqlite3.SQLiteConn), nil
}

func exec(c *sqlite3.SQLiteConn, q string) error {
	_, err := c.Exec(q, nil)
	return err
}

func queryOne(c *sqlite3.SQLiteConn, q string) (string, error) {
	rows, err := c.Query(q, nil)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	vals := make([]driver.Value, len(rows.Columns()))
	if err := rows.Next(vals); err != nil {
		return "", err
	}
	switch v := vals[0].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}
	return fmt.Sprint(vals[0]), nil
}

// DB is backupDb: src copied page by page into dest's temp file, switched
// out of WAL so the snapshot is one file, checked, and gzipped to dest.
func DB(src, dest string) (Result, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Result{}, err
	}
	return snapshot(src, fmt.Sprintf("%s.tmp-%d", dest, os.Getpid()), func(tmp string) (int64, error) {
		if err := gzipFile(tmp, dest); err != nil {
			return 0, err
		}
		out, err := os.Stat(dest)
		if err != nil {
			return 0, err
		}
		return out.Size(), nil
	}, dest)
}

// ToWriter is DB with the gzip written to w (`recall backup -`), so a host
// script can stream a snapshot out of a container that has no shell. The
// snapshot file sits next to the database while it is checked.
func ToWriter(src string, w io.Writer) (Result, error) {
	tmp := filepath.Join(filepath.Dir(src), fmt.Sprintf(".recall-backup.tmp-%d", os.Getpid()))
	return snapshot(src, tmp, func(tmp string) (int64, error) {
		in, err := os.Open(tmp)
		if err != nil {
			return 0, err
		}
		defer in.Close()
		cw := &countWriter{w: w}
		zw, _ := gzip.NewWriterLevel(cw, 9)
		if _, err := io.Copy(zw, in); err != nil {
			return 0, err
		}
		if err := zw.Close(); err != nil {
			return 0, err
		}
		return cw.n, nil
	}, "-")
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// snapshot copies src to tmp with the online backup API, checks it, and
// hands it to write; tmp is removed after.
func snapshot(src, tmp string, write func(tmp string) (int64, error), dest string) (Result, error) {
	defer func() {
		for _, f := range []string{tmp, tmp + "-wal", tmp + "-shm"} {
			os.Remove(f)
		}
	}()
	if _, err := os.Stat(src); err != nil {
		return Result{}, err
	}
	// A plain read-only connection: no migrations. The backup copies pages,
	// so vec0 and FTS5 tables come along as they are.
	live, err := open("file:" + escape(src) + "?mode=ro&_busy_timeout=5000")
	if err != nil {
		return Result{}, err
	}
	snap, err := open("file:" + escape(tmp))
	if err != nil {
		live.Close()
		return Result{}, err
	}
	b, err := snap.Backup("main", live, "main")
	if err == nil {
		for {
			done, serr := b.Step(-1)
			if serr != nil {
				err = serr
				break
			}
			if done {
				break
			}
		}
		if ferr := b.Finish(); err == nil {
			err = ferr
		}
	}
	live.Close()
	if err == nil {
		// The copy inherits WAL mode; the app turns it on again when it opens it.
		err = exec(snap, "PRAGMA journal_mode = DELETE")
	}
	if err == nil {
		var check string
		if check, err = queryOne(snap, "PRAGMA quick_check"); err == nil && check != "ok" {
			err = errors.New("snapshot failed quick_check: " + check)
		}
	}
	snap.Close()
	if err != nil {
		return Result{}, err
	}
	st, err := os.Stat(tmp)
	if err != nil {
		return Result{}, err
	}
	n, err := write(tmp)
	if err != nil {
		return Result{}, err
	}
	return Result{Dest: dest, Bytes: n, DBBytes: st.Size()}, nil
}

// Prune keeps the newest keep files in dir whose names start with prefix
// (names sort by their UTC timestamp) and deletes the rest, returning their
// names. Snapshots still being written (".tmp-") are left alone.
func Prune(dir, prefix string, keep int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, prefix) && !strings.Contains(n, ".tmp-") {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var removed []string
	for i, n := range names {
		if i < keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, n)); err != nil {
			return removed, err
		}
		removed = append(removed, n)
	}
	return removed, nil
}

func escape(p string) string {
	return (&url.URL{Path: p}).EscapedPath()
}

func gzipFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	zw, _ := gzip.NewWriterLevel(out, 9)
	if _, err := io.Copy(zw, in); err != nil {
		out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
