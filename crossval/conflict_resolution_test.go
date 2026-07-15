package crossval

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
	_ "modernc.org/sqlite"
)

// TestConflictDefaultResolution verifies what value lands in the merged database
// for same-cell conflicts across both Rebase paths.
//
// Path 1: geodiff.Rebase() — in-place DB modification
// Path 2: geodiff.CreateRebasedChangeset() — produces rebased changeset + conflict JSON
func TestConflictDefaultResolution(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "geodiff-conflict-resolve-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Build base GPKG with one row.
	base := filepath.Join(tmpDir, "base.gpkg")
	createSimpleGPKG(t, base, "test", []row{{fid: 1, name: "original", value: 10}})

	// theirs: update name → "theirs_name"
	theirs := filepath.Join(tmpDir, "theirs.gpkg")
	copyFileData(readFile(t, base), theirs)
	execSQL(t, theirs, "UPDATE test SET name = 'theirs_name' WHERE fid = 1")

	// ours: update name → "ours_name" (same cell → conflict)
	ours := filepath.Join(tmpDir, "ours.gpkg")
	copyFileData(readFile(t, base), ours)
	execSQL(t, ours, "UPDATE test SET name = 'ours_name' WHERE fid = 1")

	// --- Path 1: geodiff.Rebase() — in-place DB modification ---
	oursForRebase := filepath.Join(tmpDir, "ours_rebase.gpkg")
	copyFileData(readFile(t, ours), oursForRebase)
	conflictFile1 := filepath.Join(tmpDir, "conflicts1.json")
	err1 := geodiff.Rebase(base, theirs, oursForRebase, conflictFile1)

	t.Logf("Path 1 (Rebase in-place): err=%v", err1)

	if err1 != nil {
		// Rebase fails on conflict — DB is NOT modified. Verify.
		db, _ := sql.Open("sqlite", oursForRebase+"?mode=ro")
		defer db.Close()
		var name string
		db.QueryRow("SELECT name FROM test WHERE fid = 1").Scan(&name)
		t.Logf("  DB after failed Rebase: name=%q (expected unchanged 'ours_name')", name)
	}

	// --- Path 2: CreateRebasedChangeset — produces changeset file ---
	base2theirs, err := createDiffFile(t, base, theirs, tmpDir, "base2theirs")
	if err != nil {
		t.Fatal(err)
	}
	rebased := filepath.Join(tmpDir, "theirs2merged.bin")
	conflictFile2 := filepath.Join(tmpDir, "conflicts2.json")
	err2 := geodiff.CreateRebasedChangeset(base, ours, base2theirs, rebased, conflictFile2)

	t.Logf("Path 2 (CreateRebasedChangeset): err=%v", err2)

	if err2 == nil {
		conflictData, _ := os.ReadFile(conflictFile2)
		if len(conflictData) > 0 {
			t.Logf("  Conflict JSON:\n%s", string(conflictData))
		}
		// Apply rebased changeset to theirs → check resulting value.
		theirsForApply := filepath.Join(tmpDir, "theirs_apply.gpkg")
		copyFileData(readFile(t, theirs), theirsForApply)
		if err := geodiff.ApplyChangeset(theirsForApply, rebased); err != nil {
			t.Logf("  Apply rebased to theirs: %v", err)
		} else {
			db, _ := sql.Open("sqlite", theirsForApply+"?mode=ro")
			defer db.Close()
			var name string
			db.QueryRow("SELECT name FROM test WHERE fid = 1").Scan(&name)
			t.Logf("  DB after theirs + rebased apply: name=%q", name)
		}
	}
}

func createDiffFile(t *testing.T, base, modified, dir, name string) (string, error) {
	t.Helper()
	path := filepath.Join(dir, name+".bin")
	return path, geodiff.CreateChangeset(base, modified, path)
}
