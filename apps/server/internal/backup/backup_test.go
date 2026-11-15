package backup_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/backup"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/testkit"
)

func TestMain(m *testing.M) {
	testkit.Setup()
	os.Exit(m.Run())
}

func TestToWriterAndPrune(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "recall.db")
	d, err := db.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	var buf bytes.Buffer
	res, err := backup.ToWriter(src, &buf)
	if err != nil || res.Bytes != int64(buf.Len()) || res.DBBytes == 0 {
		t.Fatal(res, err)
	}
	zr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 16)
	io.ReadFull(zr, head)
	if string(head) != "SQLite format 3\x00" {
		t.Fatalf("%q", head)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, ".recall-backup.tmp-*")); len(m) != 0 {
		t.Fatal(m)
	}
	bk := filepath.Join(dir, "backups")
	os.MkdirAll(bk, 0o755)
	for _, n := range []string{"pre-deploy-20261101T000000Z.db.gz", "pre-deploy-20261102T000000Z.db.gz", "pre-deploy-20261103T000000Z.db.gz", "pre-deploy-20261104T000000Z.db.gz", "recall-20261101.db.gz", "pre-deploy-20261105T000000Z.db.gz.tmp-9"} {
		os.WriteFile(filepath.Join(bk, n), []byte("x"), 0o644)
	}
	removed, err := backup.Prune(bk, "pre-deploy-", 3)
	if err != nil || len(removed) != 1 || removed[0] != "pre-deploy-20261101T000000Z.db.gz" {
		t.Fatal(removed, err)
	}
	left, _ := os.ReadDir(bk)
	if len(left) != 5 {
		t.Fatal(len(left))
	}
}
