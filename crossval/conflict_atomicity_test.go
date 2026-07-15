package crossval

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
	_ "modernc.org/sqlite"
)

// TestConflictAtomicity verifies whether Rebase() applies non-conflicting changes
// or fails atomically when any conflict exists. This is the operational difference
// between "one bad row blocks the whole survey push" and "clean edits land, conflicts wait."
func TestConflictAtomicity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "geodiff-atomicity-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Base: two rows — row 1 will conflict, row 2 is clean.
	base := filepath.Join(tmpDir, "base.gpkg")
	createSimpleGPKG(t, base, "finds", []row{
		{fid: 1, name: "original_1", value: 10},
		{fid: 2, name: "original_2", value: 20},
	})

	// theirs: update row 1 name → "theirs_name" (conflict with ours)
	theirs := filepath.Join(tmpDir, "theirs.gpkg")
	copyFileData(readFile(t, base), theirs)
	execSQL(t, theirs, "UPDATE finds SET name = 'theirs_name' WHERE fid = 1")

	// ours: update row 1 name → "ours_name" (CONFLICT), update row 2 value → 99 (CLEAN)
	ours := filepath.Join(tmpDir, "ours.gpkg")
	copyFileData(readFile(t, base), ours)
	execSQL(t, ours, "UPDATE finds SET name = 'ours_name' WHERE fid = 1")
	execSQL(t, ours, "UPDATE finds SET value = 99 WHERE fid = 2")

	conflictFile := filepath.Join(tmpDir, "conflicts.json")
	err = geodiff.Rebase(base, theirs, ours, conflictFile)

	t.Logf("Rebase err: %v", err)

	if err != nil {
		// Atomic failure — check that row 2's clean edit was NOT applied.
		db, _ := sql.Open("sqlite", ours+"?mode=ro")
		defer db.Close()

		var row1Name string
		var row2Value int
		db.QueryRow("SELECT name FROM finds WHERE fid = 1").Scan(&row1Name)
		db.QueryRow("SELECT value FROM finds WHERE fid = 2").Scan(&row2Value)

		t.Logf("After atomic failure:")
		t.Logf("  Row 1 name: %q (was 'ours_name', should be unchanged)", row1Name)
		t.Logf("  Row 2 value: %d (was 99, should be 20 if atomic rollback, 99 if partial)", row2Value)

		if row2Value == 20 {
			t.Log("✅ Atomic — clean row 2 edit was rolled back with the conflict")
		} else if row2Value == 99 {
			t.Log("⚠️  Partial — clean row 2 edit survived despite conflict on row 1")
		}
	} else {
		t.Log("✅ No error — all changes applied (unexpected for conflict case)")
	}

	conflictData, _ := os.ReadFile(conflictFile)
	if len(conflictData) > 0 {
		t.Logf("Conflict file:\n%s", string(conflictData))
	}
}
