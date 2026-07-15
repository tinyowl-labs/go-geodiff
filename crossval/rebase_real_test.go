package crossval

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
	_ "modernc.org/sqlite"
)

// TestRebaseNonConflictDifferentValues verifies the core claim: when two
// branches make genuinely different, non-identical edits to different
// columns of the same row (the ordinary non-conflicting sync case), does
// CreateRebasedChangeset produce a changeset containing our edits?
//
// This directly tests whether the #4 finding (rebaseChangesets processing
// the wrong side's entries) causes real data loss.
func TestRebaseNonConflictDifferentValues(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "geodiff-rebase-real-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Base: one row.
	base := filepath.Join(tmpDir, "base.gpkg")
	createSimpleGPKG(t, base, "finds", []row{{fid: 1, name: "original", value: 10}})

	// theirs: update value 10 → 50 (different column from ours)
	theirs := filepath.Join(tmpDir, "theirs.gpkg")
	copyFileData(readFile(t, base), theirs)
	execSQL(t, theirs, "UPDATE finds SET value = 50 WHERE fid = 1")

	// ours: update name "original" → "ours_name" (different column from theirs)
	ours := filepath.Join(tmpDir, "ours.gpkg")
	copyFileData(readFile(t, base), ours)
	execSQL(t, ours, "UPDATE finds SET name = 'ours_name' WHERE fid = 1")

	// Create base→theirs diff.
	diffBaseTheirs := filepath.Join(tmpDir, "base2theirs.bin")
	if err := geodiff.CreateChangeset(base, theirs, diffBaseTheirs); err != nil {
		t.Fatal(err)
	}

	// CreateRebasedChangeset: rebase ours on top of theirs.
	rebased := filepath.Join(tmpDir, "theirs2merged.bin")
	conflictFile := filepath.Join(tmpDir, "conflicts.json")
	if err := geodiff.CreateRebasedChangeset(base, ours, diffBaseTheirs, rebased, conflictFile); err != nil {
		t.Fatalf("CreateRebasedChangeset: %v", err)
	}

	// Check rebased output.
	fi, _ := os.Stat(rebased)
	t.Logf("Rebased changeset: %d bytes", fi.Size())

	// Apply: theirs + rebased → should have both edits.
	merged := filepath.Join(tmpDir, "merged.gpkg")
	copyFileData(readFile(t, base), merged)
	if err := geodiff.ApplyChangeset(merged, diffBaseTheirs); err != nil {
		t.Fatal(err)
	}
	if err := geodiff.ApplyChangeset(merged, rebased); err != nil {
		t.Fatalf("apply rebased: %v (rebased=%d bytes)", err, fi.Size())
	}

	// Verify: both edits landed.
	db, _ := sql.Open("sqlite", merged+"?mode=ro")
	defer db.Close()

	var name string
	var value int
	db.QueryRow("SELECT name, value FROM finds WHERE fid = 1").Scan(&name, &value)

	t.Logf("Merged: name=%q value=%d", name, value)

	if name == "ours_name" && value == 50 {
		t.Log("✅ Both edits landed — rebase correctly propagated our non-conflicting edit")
	} else if name == "original" && value == 50 {
		t.Error("❌ Our edit was DROPPED — rebase output is missing our change")
	} else if name == "ours_name" && value == 10 {
		t.Error("❌ Their edit was dropped (theirs value overwritten)")
	} else {
		t.Errorf("❌ Unexpected state: name=%q value=%d", name, value)
	}

	// Check for spurious conflicts.
	conflictData, _ := os.ReadFile(conflictFile)
	if len(conflictData) > 0 {
		t.Errorf("❌ Unexpected conflict for non-conflicting edits:\n%s", string(conflictData))
	}
}
