package crossval

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/changeset"
	"github.com/tinyowl-labs/go-geodiff/geodiff"
	_ "modernc.org/sqlite"
)

// TestRebaseDirectApply verifies that a rebased changeset (our-only changes)
// applies correctly to a base that already has theirs changes.
func TestRebaseDirectApply(t *testing.T) {
	dir := t.TempDir()

	// Build a simple GPKG.
	gpkg := filepath.Join(dir, "test.gpkg")
	db, err := sql.Open("sqlite", gpkg+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE finds (fid INTEGER PRIMARY KEY, name TEXT, value INTEGER)")
	db.Exec("INSERT INTO finds VALUES (1, 'original', 10)")
	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	db.Close()

	// theirs: UPDATE value=10 → 50
	theirs := filepath.Join(dir, "theirs.bin")
	table := changeset.ChangesetTable{Name: "finds", PrimaryKeys: []bool{true, false, false}}
	w, _ := changeset.NewWriter(theirs)
	w.BeginTable(table)
	w.WriteEntry(changeset.ChangesetEntry{
		Op: changeset.OpUpdate,
		OldValues: []changeset.Value{
			changeset.NewValueInt(1),
			changeset.NewValueUndefined(),
			changeset.NewValueInt(10),
		},
		NewValues: []changeset.Value{
			changeset.NewValueUndefined(),
			changeset.NewValueUndefined(),
			changeset.NewValueInt(50),
		},
	})
	w.Close()

	// rebased: UPDATE name='original' → 'ours_name' (our edit only)
	rebased := filepath.Join(dir, "rebased.bin")
	w2, _ := changeset.NewWriter(rebased)
	w2.BeginTable(table)
	w2.WriteEntry(changeset.ChangesetEntry{
		Op: changeset.OpUpdate,
		OldValues: []changeset.Value{
			changeset.NewValueInt(1),
			changeset.NewValueText("original"),
			changeset.NewValueUndefined(),
		},
		NewValues: []changeset.Value{
			changeset.NewValueUndefined(),
			changeset.NewValueText("ours_name"),
			changeset.NewValueUndefined(),
		},
	})
	w2.Close()

	// Apply theirs first.
	t.Logf("GPKG has table finds with (1, 'original', 10)")
	fi, _ := os.Stat(theirs)
	t.Logf("theirs changeset: %d bytes", fi.Size())
	if err := geodiff.ApplyChangeset(gpkg, theirs); err != nil {
		t.Logf("apply theirs: %v", err)
	}

	// Apply rebased.
	if err := geodiff.ApplyChangeset(gpkg, rebased); err != nil {
		t.Fatalf("apply rebased: %v", err)
	}

	// Verify.
	db2, _ := sql.Open("sqlite", gpkg+"?mode=ro")
	defer db2.Close()
	var name string
	var value int
	db2.QueryRow("SELECT name, value FROM finds WHERE fid = 1").Scan(&name, &value)
	t.Logf("Result: name=%q value=%d", name, value)
	if name != "ours_name" || value != 50 {
		t.Errorf("expected (ours_name, 50), got (%q, %d)", name, value)
	}
}
