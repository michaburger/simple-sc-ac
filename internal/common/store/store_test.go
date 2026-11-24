package store

import (
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestMigrations(t *testing.T) {
	file := filepath.Join(t.TempDir(), "x.sqlite")
	m := fstest.MapFS{
		"001_init.sql": {Data: []byte("CREATE TABLE a (x INTEGER);")},
		"002_more.sql": {Data: []byte("ALTER TABLE a ADD COLUMN y TEXT;")},
	}
	db, err := Open(file, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO a VALUES (?, ?)`, uint64(1)<<62, "z"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	// reopen: nothing applied twice, a new migration is added
	m["003_idx.sql"] = &fstest.MapFile{Data: []byte("CREATE INDEX a_x ON a(x);")}
	db, err = Open(file, m)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM schema_migration`).Scan(&n)
	if n != 3 {
		t.Fatalf("%d migrations recorded", n)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "y.sqlite"), fstest.MapFS{"bad.sql": {Data: []byte("x")}}); err == nil {
		t.Fatal("bad migration name accepted")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "z.sqlite"), fstest.MapFS{"001_x.sql": {Data: []byte("NOT SQL")}}); err == nil {
		t.Fatal("broken migration accepted")
	}
}
