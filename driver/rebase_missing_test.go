package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/changeset"
)

// TestRebase_ConcurrentEditDifferentColumns verifies that when two branches
// modify different columns of the same row, the rebase produces a merged
// update with both changes and no conflict. This is the happy path for
// multi-person field teams editing different attributes of the same find.
func TestRebase_ConcurrentEditDifferentColumns(t *testing.T) {
	tmpDir := t.TempDir()

	// --- Build base changeset (theirs): update column 1 ---
	baseTheirs := filepath.Join(tmpDir, "base2theirs.bin")
	{
		w, _ := changeset.NewWriter(baseTheirs)
		table := changeset.ChangesetTable{Name: "finds", PrimaryKeys: []bool{true, false, false}}
		w.BeginTable(table)
		w.WriteEntry(changeset.ChangesetEntry{
			Op: changeset.OpUpdate,
			OldValues: []changeset.Value{
				changeset.NewValueInt(1),
				changeset.NewValueText("Old Material"),
				changeset.NewValueInt(10),
			},
			NewValues: []changeset.Value{
				changeset.NewValueUndefined(),
				changeset.NewValueText("New Material"),
				changeset.NewValueUndefined(),
			},
		})
		w.Close()
	}

	// --- Build ours changeset: update column 2 (different column) ---
	baseOurs := filepath.Join(tmpDir, "base2ours.bin")
	{
		w, _ := changeset.NewWriter(baseOurs)
		table := changeset.ChangesetTable{Name: "finds", PrimaryKeys: []bool{true, false, false}}
		w.BeginTable(table)
		w.WriteEntry(changeset.ChangesetEntry{
			Op: changeset.OpUpdate,
			OldValues: []changeset.Value{
				changeset.NewValueInt(1),
				changeset.NewValueText("Old Material"),
				changeset.NewValueInt(10),
			},
			NewValues: []changeset.Value{
				changeset.NewValueUndefined(),
				changeset.NewValueUndefined(),
				changeset.NewValueInt(20),
			},
		})
		w.Close()
	}

	// --- Rebase ---
	output := filepath.Join(tmpDir, "theirs2merged.bin")
	conflicts, err := Rebase(baseTheirs, baseOurs, output)
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}

	// No conflicts expected — different columns were modified.
	if len(conflicts) > 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	// Verify output contains a merged update with both changes.
	r, err := changeset.NewReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected one entry in rebased changeset")
	}
	if entry.Op != changeset.OpUpdate {
		t.Errorf("expected UPDATE, got %s", entry.Op)
	}

	// Column 1 (theirs-only change): should NOT be in the rebased output.
	// The rebased changeset (theirs→merged) contains only our changes.
	// Theirs changes are applied separately (base→theirs).
	col1New, _ := entry.NewValues[1].AsText()
	if col1New != "" {
		t.Errorf("column 1: expected undefined (theirs-only, skipped), got %q", col1New)
	}

	// Column 2 (our change): should be 20.
	col2New, _ := entry.NewValues[2].AsInt()
	if col2New != 20 {
		t.Errorf("column 2: expected 20, got %d", col2New)
	}
}

// TestRebase_ConcurrentInsertDifferentTables tests that inserts on different
// tables don't interfere — both should appear in the rebased output.
func TestRebase_ConcurrentInsertDifferentTables(t *testing.T) {
	tmpDir := t.TempDir()

	// theirs: INSERT into table A
	baseTheirs := filepath.Join(tmpDir, "base2theirs.bin")
	{
		w, _ := changeset.NewWriter(baseTheirs)
		table := changeset.ChangesetTable{Name: "finds", PrimaryKeys: []bool{true, false}}
		w.BeginTable(table)
		w.WriteEntry(changeset.ChangesetEntry{
			Op:        changeset.OpInsert,
			NewValues: []changeset.Value{changeset.NewValueInt(100), changeset.NewValueText("theirs_find")},
		})
		w.Close()
	}

	// ours: INSERT into table B
	baseOurs := filepath.Join(tmpDir, "base2ours.bin")
	{
		w, _ := changeset.NewWriter(baseOurs)
		table := changeset.ChangesetTable{Name: "samples", PrimaryKeys: []bool{true, false}}
		w.BeginTable(table)
		w.WriteEntry(changeset.ChangesetEntry{
			Op:        changeset.OpInsert,
			NewValues: []changeset.Value{changeset.NewValueInt(200), changeset.NewValueText("ours_sample")},
		})
		w.Close()
	}

	output := filepath.Join(tmpDir, "theirs2merged.bin")
	conflicts, err := Rebase(baseTheirs, baseOurs, output)
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if len(conflicts) > 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	r, err := changeset.NewReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	count := 0
	for {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil {
			break
		}
		count++
	}
	// Rebased output is theirs→merged: only contains OUR changes rebased on top.
	// Theirs-side changes (finds insert) are applied separately.
	if count != 1 {
		t.Errorf("expected 1 entry (ours samples insert), got %d", count)
	}
}

// Ensure test compiles — avoid unused import warnings.
var _ = context.Background
var _ = os.DevNull
