package geodiff

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Standard column metadata must take the same create/apply/invert/rebase path
// as records; catalog bookkeeping such as extents must remain excluded.
func TestColumnMetadataHistory(t *testing.T) {
	dir := t.TempDir()
	base, ours, theirs := filepath.Join(dir, "base.gpkg"), filepath.Join(dir, "ours.gpkg"), filepath.Join(dir, "theirs.gpkg")
	db, err := sql.Open("sqlite", base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE gpkg_data_columns(table_name TEXT NOT NULL,column_name TEXT NOT NULL,title TEXT,PRIMARY KEY(table_name,column_name));
 INSERT INTO gpkg_data_columns VALUES('contexts','code','Context'),('finds','code','Find');
 CREATE TABLE gpkg_contents(table_name TEXT PRIMARY KEY,last_change TEXT);
 INSERT INTO gpkg_contents VALUES('contexts','old');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	copyAndExec(t, base, ours, `UPDATE gpkg_data_columns SET title='Context code' WHERE table_name='contexts'; UPDATE gpkg_contents SET last_change='new';`)
	change, inverse := filepath.Join(dir, "change.diff"), filepath.Join(dir, "inverse.diff")
	if err = CreateChangeset(base, ours, change); err != nil {
		t.Fatal(err)
	}
	if err = InvertChangeset(change, inverse); err != nil {
		t.Fatal(err)
	}
	replay := filepath.Join(dir, "replay.gpkg")
	if err = MakeCopySqlite(base, replay); err != nil {
		t.Fatal(err)
	}
	if err = ApplyChangeset(replay, change); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		db, e := sql.Open("sqlite", replay)
		if e != nil {
			t.Fatal(e)
		}
		defer db.Close()
		var title, other, stamp string
		if e = db.QueryRow(`SELECT title FROM gpkg_data_columns WHERE table_name='contexts'`).Scan(&title); e != nil {
			t.Fatal(e)
		}
		if e = db.QueryRow(`SELECT title FROM gpkg_data_columns WHERE table_name='finds'`).Scan(&other); e != nil {
			t.Fatal(e)
		}
		if e = db.QueryRow(`SELECT last_change FROM gpkg_contents`).Scan(&stamp); e != nil {
			t.Fatal(e)
		}
		if title != want || other != "Find" || stamp != "old" {
			t.Fatalf("title=%q other=%q catalog=%q", title, other, stamp)
		}
	}
	check("Context code")
	if err = ApplyChangeset(replay, inverse); err != nil {
		t.Fatal(err)
	}
	check("Context")
	copyAndExec(t, base, theirs, `UPDATE gpkg_data_columns SET title='Other label' WHERE table_name='contexts';`)
	theirDiff := filepath.Join(dir, "theirs.diff")
	if err = CreateChangeset(base, theirs, theirDiff); err != nil {
		t.Fatal(err)
	}
	conflicts, err := CreateRebasedChangeset(base, ours, theirDiff, filepath.Join(dir, "rebased.diff"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0].TableName != "gpkg_data_columns" {
		t.Fatalf("expected explicit metadata conflict: %#v", conflicts)
	}
}
