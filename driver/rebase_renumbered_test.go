package driver

import (
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/changeset"
)

func makeDeleteEntry2(tableName string, pk int, val string) changeset.ChangesetEntry {
	return changeset.ChangesetEntry{
		Op:        changeset.OpDelete,
		Table:     changeset.ChangesetTable{Name: tableName, PrimaryKeys: []bool{true, false}},
		OldValues: []changeset.Value{changeset.NewValueInt(int64(pk)), changeset.NewValueText(val)},
	}
}

func rebaseEntries(t *testing.T, theirs, ours []changeset.ChangesetEntry) ([]changeset.ChangesetEntry, []ConflictFeature) {
	t.Helper()
	dir := t.TempDir()
	baseTheirs, baseOurs, out := filepath.Join(dir, "theirs.diff"), filepath.Join(dir, "ours.diff"), filepath.Join(dir, "out.diff")
	if err := writeChangeset(baseTheirs, theirs); err != nil {
		t.Fatal(err)
	}
	if err := writeChangeset(baseOurs, ours); err != nil {
		t.Fatal(err)
	}
	conflicts, err := Rebase(baseTheirs, baseOurs, out)
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	entries, err := readEntries(out)
	if err != nil {
		t.Fatal(err)
	}
	return entries, conflicts
}

func entryPK(t *testing.T, e changeset.ChangesetEntry) int64 {
	t.Helper()
	vals := e.OldValues
	if e.Op == changeset.OpInsert {
		vals = e.NewValues
	}
	pk, err := vals[0].AsInt()
	if err != nil {
		t.Fatal(err)
	}
	return pk
}

// An insert renumbered around theirs keeps its later update: the update must
// follow it to the new key, not land on their row with the old one.
func TestRebase_UpdateFollowsRenumberedInsert(t *testing.T) {
	entries, conflicts := rebaseEntries(t,
		[]changeset.ChangesetEntry{makeInsertEntry2("t", 1, "theirs")},
		[]changeset.ChangesetEntry{makeInsertEntry2("t", 1, "ours"), makeUpdateEntry2("t", 1, "ours", "ours edited")},
	)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts: %+v", conflicts)
	}
	if len(entries) != 2 || entries[0].Op != changeset.OpInsert || entries[1].Op != changeset.OpUpdate {
		t.Fatalf("entries: %+v", entries)
	}
	if ins, upd := entryPK(t, entries[0]), entryPK(t, entries[1]); ins != 2 || upd != 2 {
		t.Fatalf("insert pk %d, update pk %d; want both 2", ins, upd)
	}
	if v, _ := entries[1].NewValues[1].AsText(); v != "ours edited" {
		t.Fatalf("update value %q", v)
	}
}

// Their edit of their own row 1 is not an edit of our new row 1: no conflict,
// and our edit stands.
func TestRebase_TheirEditOfSameNumberIsNotOurRecord(t *testing.T) {
	entries, conflicts := rebaseEntries(t,
		[]changeset.ChangesetEntry{makeInsertEntry2("t", 1, "theirs"), makeUpdateEntry2("t", 1, "theirs", "theirs edited")},
		[]changeset.ChangesetEntry{makeInsertEntry2("t", 1, "ours"), makeUpdateEntry2("t", 1, "ours", "ours edited")},
	)
	if len(conflicts) != 0 {
		t.Fatalf("false conflict: %+v", conflicts)
	}
	if len(entries) != 2 || entryPK(t, entries[1]) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	if v, _ := entries[1].NewValues[1].AsText(); v != "ours edited" {
		t.Fatalf("our edit lost: %q", v)
	}
}

// A delete of a renumbered insert removes our row, with our values.
func TestRebase_DeleteFollowsRenumberedInsert(t *testing.T) {
	entries, _ := rebaseEntries(t,
		[]changeset.ChangesetEntry{makeInsertEntry2("t", 1, "theirs"), makeUpdateEntry2("t", 1, "theirs", "theirs edited")},
		[]changeset.ChangesetEntry{makeInsertEntry2("t", 1, "ours"), makeDeleteEntry2("t", 1, "ours")},
	)
	if len(entries) != 2 || entries[1].Op != changeset.OpDelete || entryPK(t, entries[1]) != 2 {
		t.Fatalf("entries: %+v", entries)
	}
	if v, _ := entries[1].OldValues[1].AsText(); v != "ours" {
		t.Fatalf("delete old value %q, want ours", v)
	}
}
