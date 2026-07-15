package crossval

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
	_ "modernc.org/sqlite"
)

// TestConflictPostConflictDrop verifies whether entries AFTER a conflict point
// in a multi-entry changeset are applied or silently dropped. This is the
// scenario: changeset with [A(clean), B(clean), C(conflict), D(clean)].
// If processing stops at C, D's edit vanishes with no error/conflict report.
func TestConflictPostConflictDrop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "geodiff-postconflict-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Build a GPKG with pre-existing rows, then make edits that produce
	// a multi-entry changeset where one row conflicts.
	base := filepath.Join(tmpDir, "base.gpkg")
	createSimpleGPKG(t, base, "finds", []row{
		{fid: 1, name: "one", value: 10},
		{fid: 2, name: "two", value: 20},
		{fid: 3, name: "three", value: 30},
		{fid: 4, name: "four", value: 40},
	})

	// theirs: update row 3 → conflict with ours
	theirs := filepath.Join(tmpDir, "theirs.gpkg")
	copyFileData(readFile(t, base), theirs)
	execSQL(t, theirs, "UPDATE finds SET name = 'theirs_three' WHERE fid = 3")

	// ours: update row 1 (clean), row 2 (clean), row 3 (CONFLICT), row 4 (clean)
	ours := filepath.Join(tmpDir, "ours.gpkg")
	copyFileData(readFile(t, base), ours)
	execSQL(t, ours, "UPDATE finds SET name = 'ours_one' WHERE fid = 1")   // A: clean
	execSQL(t, ours, "UPDATE finds SET value = 99 WHERE fid = 2")          // B: clean
	execSQL(t, ours, "UPDATE finds SET name = 'ours_three' WHERE fid = 3") // C: CONFLICT
	execSQL(t, ours, "UPDATE finds SET value = 88 WHERE fid = 4")          // D: clean (after conflict)

	conflictFile := filepath.Join(tmpDir, "conflicts.json")
	err = geodiff.Rebase(base, theirs, ours, conflictFile)
	t.Logf("Rebase err: %v", err)

	// Check all rows via a fresh connection (verifies durability).
	db, err := sql.Open("sqlite", ours+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type rowState struct {
		fid      int
		name     string
		value    int
		expected string // what we expect to find
		status   string // clean/conflict/dropped
	}
	rows := []rowState{
		{1, "", 0, "ours_one", "clean-before-conflict"},
		{2, "", 99, "value=99", "clean-before-conflict"},
		{3, "ours_three", 30, "unchanged (conflict)", "conflict"},
		{4, "", 88, "value=88", "clean-after-conflict"},
	}

	t.Logf("Post-rebase state (fresh connection):")
	for _, r := range rows {
		db.QueryRow("SELECT name, value FROM finds WHERE fid = ?", r.fid).Scan(&r.name, &r.value)
		ok := "✅"
		if r.fid == 3 {
			// Row 3: ours edit should NOT have landed (conflict)
			ok = "✅"
			if r.name == "ours_three" {
				ok = "❌ CONFLICT ROW WRONGLY APPLIED"
			}
		} else if r.fid == 4 {
			// Row 4: critical check — did it land or get dropped?
			if r.value == 88 {
				ok = "✅"
			} else {
				ok = "❌ SILENTLY DROPPED"
			}
		}
		t.Logf("  Row %d: name=%q value=%d  %s", r.fid, r.name, r.value, ok)
	}
}

// TestSavepointRollback verifies whether SAVEPOINT rollback actually works
// with modernc.org/sqlite, independent of go-geodiff's apply logic.
func TestSavepointRollback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite", dbPath+"?mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Exec("CREATE TABLE t (id INTEGER PRIMARY KEY, val TEXT)")
	db.Exec("INSERT INTO t VALUES (1, 'original')")

	// SAVEPOINT, modify, ROLLBACK.
	db.Exec("SAVEPOINT sp1")
	db.Exec("UPDATE t SET val = 'modified' WHERE id = 1")

	var val string
	db.QueryRow("SELECT val FROM t WHERE id = 1").Scan(&val)
	t.Logf("After UPDATE (within savepoint): val=%q", val)

	_, err = db.Exec("ROLLBACK TO sp1")
	t.Logf("ROLLBACK TO sp1 err: %v", err)

	db.QueryRow("SELECT val FROM t WHERE id = 1").Scan(&val)
	t.Logf("After ROLLBACK: val=%q", val)

	_, err = db.Exec("RELEASE sp1")
	t.Logf("RELEASE sp1 err: %v", err)

	// Fresh connection to verify durability.
	db2, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	db2.QueryRow("SELECT val FROM t WHERE id = 1").Scan(&val)
	t.Logf("Fresh connection: val=%q (expected 'original')", val)

	if val != "original" {
		t.Errorf("SAVEPOINT rollback FAILED: got %q, want 'original'", val)
	}
}
