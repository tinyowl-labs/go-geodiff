package geodiff

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyPreservesDatabase(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.sqlite")
	dst := filepath.Join(dir, "copy.sqlite")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA user_version=42;
 CREATE TABLE records(id INTEGER PRIMARY KEY, name TEXT DEFAULT 'unnamed', parent INTEGER REFERENCES records(id), CHECK(length(name)>0));
 CREATE INDEX records_name ON records(name);
 CREATE TABLE unkeyed(value TEXT); INSERT INTO unkeyed(rowid,value) VALUES(42,'keep me');
 INSERT INTO records(id,name) VALUES(1,'one');
 CREATE VIEW record_names AS SELECT name FROM records;
 CREATE TRIGGER validate_record BEFORE INSERT ON records WHEN NEW.name='invalid' BEGIN SELECT RAISE(ABORT,'invalid'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	// Leave the source connection open: the backup must include committed WAL data.
	if err = MakeCopySqlite(src, dst); err != nil {
		t.Fatal(err)
	}
	out, err := sql.Open("sqlite", dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	schemaSQL := func(db *sql.DB) string {
		rows, e := db.Query("SELECT sql FROM sqlite_master WHERE sql IS NOT NULL ORDER BY name")
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		var s []string
		for rows.Next() {
			var v string
			if e = rows.Scan(&v); e != nil {
				t.Fatal(e)
			}
			s = append(s, v)
		}
		return strings.Join(s, "\n")
	}
	if got, want := schemaSQL(out), schemaSQL(db); got != want {
		t.Errorf("schema changed:\n%s\nwant:\n%s", got, want)
	}
	var rowid, version int
	var value string
	if err = out.QueryRow("SELECT rowid,value FROM unkeyed").Scan(&rowid, &value); err != nil || rowid != 42 || value != "keep me" {
		t.Errorf("unkeyed row lost: %d %q %v", rowid, value, err)
	}
	if err = out.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 42 {
		t.Errorf("user_version=%d err=%v", version, err)
	}
}

func TestCopyFailurePreservesDestination(t *testing.T) {
	d := t.TempDir()
	src := filepath.Join(d, "invalid")
	dst := filepath.Join(d, "destination")
	os.WriteFile(src, []byte("not sqlite"), 0600)
	os.WriteFile(dst, []byte("keep destination"), 0600)
	if err := MakeCopySqlite(src, dst); err == nil {
		t.Fatal("expected invalid database error")
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "keep destination" {
		t.Fatalf("destination replaced on error: %q %v", b, err)
	}
}

func BenchmarkCopyDatabase(b *testing.B) {
	d := b.TempDir()
	src := filepath.Join(d, "source.sqlite")
	dst := filepath.Join(d, "copy.sqlite")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		b.Fatal(err)
	}
	db.Exec("CREATE TABLE records(id INTEGER PRIMARY KEY,payload TEXT)")
	tx, _ := db.Begin()
	stmt, _ := tx.Prepare("INSERT INTO records VALUES(?,?)")
	payload := strings.Repeat("x", 1024)
	for i := 0; i < 10000; i++ {
		if _, err = stmt.Exec(i, payload); err != nil {
			b.Fatal(err)
		}
	}
	stmt.Close()
	tx.Commit()
	db.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err = MakeCopySqlite(src, dst); err != nil {
			b.Fatal(err)
		}
	}
}
