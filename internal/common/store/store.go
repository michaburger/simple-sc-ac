// Package store opens a SQLite file (pure Go driver, no cgo) and applies numbered SQL
// migrations. Each program keeps its own file and its own migrations.
package store

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

// Open opens or creates the database at file, sets WAL mode and a busy timeout, and
// applies all migrations in migrations (files NNN_name.sql) that are not applied yet.
func Open(file string, migrations fs.FS) (*sql.DB, error) {
	dsn := file + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite writes are serial anyway, this avoids SQLITE_BUSY
	if err := Migrate(db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate applies the migrations in order, each in its own transaction.
func Migrate(db *sql.DB, migrations fs.FS) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migration (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_ns INTEGER NOT NULL)`); err != nil {
		return err
	}
	names, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return err
	}
	type mig struct {
		v    int
		name string
	}
	var list []mig
	for _, n := range names {
		base := path.Base(n)
		num, _, ok := strings.Cut(base, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return fmt.Errorf("store: migration %s: name must be NNN_name.sql", base)
		}
		list = append(list, mig{v, n})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v < list[j].v })
	for i, m := range list {
		if i > 0 && list[i-1].v == m.v {
			return fmt.Errorf("store: two migrations with number %d", m.v)
		}
		var done int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migration WHERE version = ?`, m.v).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		body, err := fs.ReadFile(migrations, m.name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migration VALUES (?, ?, strftime('%s','now') * 1000000000)`, m.v, path.Base(m.name)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
