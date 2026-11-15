package db

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/strx"
)

// The hand-written migrations, byte for byte the files in apps/app/drizzle
// (TestEmbeddedMigrationsMatchTheTSCopies keeps them equal while those exist).
// Never rename or edit one that shipped: _migrations records them by name.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is one file: its name without ".sql" and its SQL.
type Migration struct {
	Name string
	SQL  string
}

// Migrations returns every bundled migration in file-name order, the order
// scripts/bundle-migrations.ts writes (both 0006 files included).
func Migrations() []Migration {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	out := make([]Migration, 0, len(names))
	for _, n := range names {
		b, err := migrationFiles.ReadFile("migrations/" + n)
		if err != nil {
			panic(err)
		}
		out = append(out, Migration{Name: strings.TrimSuffix(n, ".sql"), SQL: string(b)})
	}
	return out
}

// Migrate applies the migrations in list that _migrations doesn't name, each
// in one transaction, statements split on "--> statement-breakpoint"
// (migrate.ts).
func Migrate(d *DB, list []Migration) error {
	if _, err := d.W.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
    name TEXT PRIMARY KEY,
    applied_at INTEGER NOT NULL
  )`); err != nil {
		return err
	}
	applied := map[string]bool{}
	rows, err := d.W.Query("SELECT name FROM _migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return err
		}
		applied[n] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range list {
		if applied[m.Name] {
			continue
		}
		err := d.Tx(func(tx Q) error {
			for _, stmt := range strings.Split(m.SQL, "--> statement-breakpoint") {
				if strx.Trim(stmt) == "" {
					continue
				}
				if _, err := tx.Exec(stmt); err != nil {
					return err
				}
			}
			_, err := tx.Exec("INSERT INTO _migrations (name, applied_at) VALUES (?, ?)", m.Name, time.Now().UnixMilli())
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %s: %w", m.Name, err)
		}
	}
	return nil
}

// UnknownMigrations lists migrations recorded in the database that list
// doesn't have, by name. Non-empty means a newer build migrated this file.
func UnknownMigrations(q Q, list []Migration) ([]string, error) {
	known := map[string]bool{}
	for _, m := range list {
		known[m.Name] = true
	}
	rows, err := q.Query("SELECT name FROM _migrations ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if !known[n] {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}
