package geodiff

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

// The SQLite driver decodes declared dates/booleans to Go types unless queries
// explicitly preserve SQL values. Exercise inserts, updates, deletes and revert.
func TestTypedValuesPreserveSQLRepresentation(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base.sqlite")
	modified := filepath.Join(dir, "modified.sqlite")
	db, err := sql.Open("sqlite", base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE records(fid INTEGER PRIMARY KEY,d DATE,dt DATETIME,flag BOOLEAN);
 INSERT INTO records VALUES(1,'2026-09-24','2026-09-24T10:20:30.123Z',1);
 INSERT INTO records VALUES(2,'unknown','legacy value',NULL);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func(path string) [][]any {
		t.Helper()
		db, e := sql.Open("sqlite", path)
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		rows, e := db.Query(`SELECT fid,+d,+dt,+flag,typeof(d),typeof(dt),typeof(flag) FROM records ORDER BY fid`)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		var out [][]any
		for rows.Next() {
			vals := make([]any, 7)
			ptr := make([]any, 7)
			for i := range vals {
				ptr[i] = &vals[i]
			}
			if e = rows.Scan(ptr...); e != nil {
				t.Fatal(e)
			}
			out = append(out, vals)
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		return out
	}
	before := snapshot(base)
	copyAndExec(t, base, modified, `UPDATE records SET d='2026-09-25',dt='2026-09-25T12:30:01.456Z',flag=0 WHERE fid=1;
 DELETE FROM records WHERE fid=2;
 INSERT INTO records VALUES(3,NULL,'2026-09-26T00:00:00Z',1);`)
	after := snapshot(modified)
	change, inverse := filepath.Join(dir, "change.diff"), filepath.Join(dir, "inverse.diff")
	if err = CreateChangeset(base, modified, change); err != nil {
		t.Fatal(err)
	}
	if err = ApplyChangeset(base, change); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(base); !reflect.DeepEqual(got, after) {
		t.Fatalf("apply changed SQL values: %#v want %#v", got, after)
	}
	if err = InvertChangeset(change, inverse); err != nil {
		t.Fatal(err)
	}
	if err = ApplyChangeset(base, inverse); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(base); !reflect.DeepEqual(got, before) {
		t.Fatalf("inverse changed SQL values: %#v want %#v", got, before)
	}
}
