package driver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/changeset"
)

// --- helpers for creating test changesets ---

// makeInsertEntry creates an INSERT entry for a single-column (PK) table.
func makeInsertEntry(tableName string, pk int) changeset.ChangesetEntry {
	return changeset.ChangesetEntry{
		Op: changeset.OpInsert,
		Table: &changeset.ChangesetTable{
			Name:        tableName,
			PrimaryKeys: []bool{true},
		},
		NewValues: []changeset.Value{
			changeset.NewValueInt(int64(pk)),
		},
	}
}

// makeInsertEntry2 creates an INSERT for a 2-column table (PK + value col).
func makeInsertEntry2(tableName string, pk int, val string) changeset.ChangesetEntry {
	return changeset.ChangesetEntry{
		Op: changeset.OpInsert,
		Table: &changeset.ChangesetTable{
			Name:        tableName,
			PrimaryKeys: []bool{true, false},
		},
		NewValues: []changeset.Value{
			changeset.NewValueInt(int64(pk)),
			changeset.NewValueText(val),
		},
	}
}

// makeDeleteEntry creates a DELETE entry for a single-column (PK) table.
func makeDeleteEntry(tableName string, pk int) changeset.ChangesetEntry {
	return changeset.ChangesetEntry{
		Op: changeset.OpDelete,
		Table: &changeset.ChangesetTable{
			Name:        tableName,
			PrimaryKeys: []bool{true},
		},
		OldValues: []changeset.Value{
			changeset.NewValueInt(int64(pk)),
		},
	}
}

// makeUpdateEntry2 creates an UPDATE entry for a 2-column table.
func makeUpdateEntry2(tableName string, pk int, oldVal, newVal string) changeset.ChangesetEntry {
	return changeset.ChangesetEntry{
		Op: changeset.OpUpdate,
		Table: &changeset.ChangesetTable{
			Name:        tableName,
			PrimaryKeys: []bool{true, false},
		},
		OldValues: []changeset.Value{
			changeset.NewValueInt(int64(pk)),
			changeset.NewValueText(oldVal),
		},
		NewValues: []changeset.Value{
			changeset.NewValueUndefined(), // PK — not modified
			changeset.NewValueText(newVal),
		},
	}
}

// writeChangeset writes a list of entries to a changeset file.
func writeChangeset(path string, entries []changeset.ChangesetEntry) error {
	writer, err := changeset.NewWriter(path)
	if err != nil {
		return err
	}
	defer writer.Close()

	var currentTable string
	var currentPKs []bool
	for _, entry := range entries {
		// Detect table change (name or schema)
		name := entry.Table.Name
		sameSchema := len(currentPKs) == len(entry.Table.PrimaryKeys)
		if sameSchema {
			for i := range currentPKs {
				if currentPKs[i] != entry.Table.PrimaryKeys[i] {
					sameSchema = false
					break
				}
			}
		}
		if name != currentTable || !sameSchema {
			if err := writer.BeginTable(*entry.Table); err != nil {
				return err
			}
			currentTable = name
			currentPKs = entry.Table.PrimaryKeys
		}
		if err := writer.WriteEntry(entry); err != nil {
			return err
		}
	}
	return nil
}

// readEntries reads all entries from a changeset file.
// Note: the Reader reuses its internal Table pointer, so we clone each entry's Table.
func readEntries(path string) ([]changeset.ChangesetEntry, error) {
	reader, err := changeset.NewReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var entries []changeset.ChangesetEntry
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}
		// Deep-clone: Reader reuses its internal Table, so we must copy it
		cloned := entry.Clone()
		cloned.Table = entry.Table.Clone()
		entries = append(entries, *cloned)
	}
	return entries, nil
}

// --- Test: simple rebase (no conflicts) ---

func TestRebase_SimpleNoConflict(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: insert row with PK=5
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: insert row with PK=6
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 6),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	// Read merged changeset: should have insert PK=6 (ours)
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}

	// We expect 1 INSERT entry with PK=6 (ours insert passes through)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Op != changeset.OpInsert {
		t.Errorf("expected INSERT, got %s", e.Op)
	}
	if pk := e.NewValues[0].AsInt(); pk != 6 {
		t.Errorf("expected pk=6, got pk=%d", pk)
	}
}

// --- Test: concurrent insert same PK → conflict with remapping ---

func TestRebase_ConcurrentInsertConflict(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: insert PK=5
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: also insert PK=5
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	// No ConflictFeature for insert conflicts — the remapping handles it silently
	_ = conflicts

	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Op != changeset.OpInsert {
		t.Errorf("expected INSERT, got %s", e.Op)
	}

	// PK should have been remapped to max(theirs.inserted)+1 = 6
	if pk := e.NewValues[0].AsInt(); pk != 6 {
		t.Errorf("expected remapped pk=6, got pk=%d", pk)
	}
}

// --- Test: concurrent update same column → conflict recorded ---

func TestRebase_ConcurrentUpdateConflict(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: UPDATE row PK=1, name "old" → "theirs_name"
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeUpdateEntry2("test", 1, "old", "theirs_name"),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: UPDATE row PK=1, name "old" → "ours_name" (same column, different value)
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeUpdateEntry2("test", 1, "old", "ours_name"),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	// Should have a conflict feature
	if len(conflicts) == 0 {
		t.Fatal("expected a conflict, got none")
	}

	cf := conflicts[0]
	if cf.PK != 1 {
		t.Errorf("expected PK=1, got %d", cf.PK)
	}
	if cf.TableName != "test" {
		t.Errorf("expected table 'test', got %q", cf.TableName)
	}
	if len(cf.Items) != 1 {
		t.Fatalf("expected 1 conflict item, got %d", len(cf.Items))
	}
	if cf.Items[0].Column != 1 {
		t.Errorf("expected conflict on column 1, got %d", cf.Items[0].Column)
	}

	// The output entry should exist (it's an update, just with conflicts noted)
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Op != changeset.OpUpdate {
		t.Errorf("expected UPDATE, got %s", entries[0].Op)
	}
}

// --- Test: concurrent update same column to same value → no conflict ---

func TestRebase_SameUpdateNoConflict(t *testing.T) {
	tmpDir := t.TempDir()

	// Both theirs and ours update PK=1 name to "same_name" — no conflict
	// When both modify a column to the same value, there's no actual change,
	// so the entry is omitted from the rebased changeset.
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeUpdateEntry2("test", 1, "old", "same_name"),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeUpdateEntry2("test", 1, "old", "same_name"),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	// No change in rebased changeset (same value → both columns become undefined)
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries (same-update produces no changes), got %d", len(entries))
		for i, e := range entries {
			t.Logf("  entry %d: op=%s", i, e.Op)
		}
	}
}

// --- Test: concurrent delete + update → delete wins, conflict recorded ---

func TestRebase_DeleteVsUpdate(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: DELETE row PK=1
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeDeleteEntry("test", 1),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: UPDATE row PK=1, name "old" → "new_name"
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeUpdateEntry2("test", 1, "old", "new_name"),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	// Should have a conflict
	if len(conflicts) == 0 {
		t.Fatal("expected a conflict, got none")
	}

	cf := conflicts[0]
	if cf.PK != 1 {
		t.Errorf("expected PK=1, got %d", cf.PK)
	}

	// The update entry should NOT be in the output (delete wins)
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (delete wins), got %d", len(entries))
	}
}

// --- Test: empty theirs (no-op rebase) ---

func TestRebase_EmptyTheirs(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: empty
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, nil); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: insert PK=5
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	// Ours should be copied directly
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if e := entries[0]; e.Op != changeset.OpInsert || e.NewValues[0].AsInt() != 5 {
		t.Errorf("unexpected entry: %s pk=%d", e.Op, e.NewValues[0].AsInt())
	}
}

// --- Test: empty ours (no-op rebase) ---

func TestRebase_EmptyOurs(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: insert PK=5
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: empty
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, nil); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	// Theirs should be copied directly
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if e := entries[0]; e.Op != changeset.OpInsert || e.NewValues[0].AsInt() != 5 {
		t.Errorf("unexpected entry: %s pk=%d", e.Op, e.NewValues[0].AsInt())
	}
}

// --- Test: conflict JSON output ---

func TestRebase_ConflictJSON(t *testing.T) {
	tmpDir := t.TempDir()

	// BASE → THEIRS: UPDATE row PK=1, name "old" → "theirs_name", val 10 → 20
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	table3Col := &changeset.ChangesetTable{
		Name:        "test",
		PrimaryKeys: []bool{true, false, false},
	}
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		{
			Op: changeset.OpUpdate, Table: table3Col,
			OldValues: []changeset.Value{
				changeset.NewValueInt(1), changeset.NewValueText("old"), changeset.NewValueInt(10),
			},
			NewValues: []changeset.Value{
				changeset.NewValueUndefined(), changeset.NewValueText("theirs_name"), changeset.NewValueInt(20),
			},
		},
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// BASE → OURS: UPDATE row PK=1, name "old" → "ours_name", val 10 → 30
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		{
			Op: changeset.OpUpdate, Table: table3Col,
			OldValues: []changeset.Value{
				changeset.NewValueInt(1), changeset.NewValueText("old"), changeset.NewValueInt(10),
			},
			NewValues: []changeset.Value{
				changeset.NewValueUndefined(), changeset.NewValueText("ours_name"), changeset.NewValueInt(30),
			},
		},
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	if len(conflicts) == 0 {
		t.Fatal("expected conflicts, got none")
	}

	// Write to conflict file
	conflictFile := filepath.Join(tmpDir, "conflicts.json")
	if err := writeConflictFile(conflictFile, conflicts); err != nil {
		t.Fatalf("writeConflictFile: %v", err)
	}

	// Read and validate JSON
	data, err := os.ReadFile(conflictFile)
	if err != nil {
		t.Fatalf("read conflict file: %v", err)
	}
	t.Logf("Conflict JSON:\n%s", string(data))

	var cf conflictsJSON
	if err := json.Unmarshal(data, &cf); err != nil {
		t.Fatalf("unmarshal conflict JSON: %v", err)
	}

	if len(cf.Geodiff) != 1 {
		t.Fatalf("expected 1 conflict feature, got %d", len(cf.Geodiff))
	}

	f := cf.Geodiff[0]
	if f.Table != "test" {
		t.Errorf("expected table 'test', got %q", f.Table)
	}
	if f.Type != "conflict" {
		t.Errorf("expected type 'conflict', got %q", f.Type)
	}
	if f.FID != "1" {
		t.Errorf("expected fid '1', got %q", f.FID)
	}
	if len(f.Changes) != 2 {
		t.Errorf("expected 2 conflict items (columns 1 and 2), got %d", len(f.Changes))
	}

	// Verify column numbers are correct (columns 1 and 2 since PK=0 is not modified)
	cols := []int{}
	for _, ch := range f.Changes {
		cols = append(cols, ch.Column)
	}
	sort.Ints(cols)
	if len(cols) != 2 || cols[0] != 1 || cols[1] != 2 {
		t.Errorf("unexpected columns: %v", cols)
	}
}

// --- Test: gpkg_contents column 4 (last_change) not a conflict ---

func TestRebase_GpkgContentsLastChange(t *testing.T) {
	tmpDir := t.TempDir()

	// gpkg_contents has many columns; we only care that column 4 is last_change
	gpkgTable := &changeset.ChangesetTable{
		Name: "gpkg_contents",
		PrimaryKeys: []bool{
			true, false, false, false, false, false, false, false, false, false,
		},
	}

	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		{
			Op: changeset.OpUpdate, Table: gpkgTable,
			OldValues: makeRowValues(gpkgTable, 1, "features", "features", "", "2020-01-01T00:00:00.000Z",
				0.0, 0.0, 0.0, 0.0, 4326),
			NewValues: makeUpdateNewValues(gpkgTable, 4, "2021-01-01T00:00:00.000Z"),
		},
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		{
			Op: changeset.OpUpdate, Table: gpkgTable,
			OldValues: makeRowValues(gpkgTable, 1, "features", "features", "", "2020-01-01T00:00:00.000Z",
				0.0, 0.0, 0.0, 0.0, 4326),
			NewValues: makeUpdateNewValues(gpkgTable, 4, "2022-01-01T00:00:00.000Z"),
		},
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	// Column 4 conflict should be suppressed — no conflicts
	if len(conflicts) > 0 {
		t.Errorf("expected 0 conflicts for gpkg_contents col 4, got %d", len(conflicts))
		for _, cf := range conflicts {
			t.Logf("conflict: table=%s pk=%d items=%d", cf.TableName, cf.PK, len(cf.Items))
			for _, item := range cf.Items {
				t.Logf("  col=%d", item.Column)
			}
		}
	}
}

// makeRowValues creates a full oldValues slice for a table with all columns populated.
func makeRowValues(table *changeset.ChangesetTable, pk int, vals ...interface{}) []changeset.Value {
	result := make([]changeset.Value, len(table.PrimaryKeys))
	// Set PK
	for i, isPK := range table.PrimaryKeys {
		if isPK {
			result[i] = changeset.NewValueInt(int64(pk))
			break
		}
	}
	j := 0
	for i, isPK := range table.PrimaryKeys {
		if isPK {
			continue
		}
		if j < len(vals) {
			result[i] = interfaceToValue(vals[j])
			j++
		}
	}
	return result
}

// makeUpdateNewValues creates newValues for an UPDATE entry: only specified column is set.
func makeUpdateNewValues(table *changeset.ChangesetTable, col int, val interface{}) []changeset.Value {
	result := make([]changeset.Value, len(table.PrimaryKeys))
	for i := range result {
		result[i] = changeset.NewValueUndefined()
	}
	result[col] = interfaceToValue(val)
	return result
}

func interfaceToValue(v interface{}) changeset.Value {
	switch val := v.(type) {
	case int:
		return changeset.NewValueInt(int64(val))
	case int64:
		return changeset.NewValueInt(val)
	case float64:
		return changeset.NewValueDouble(val)
	case string:
		return changeset.NewValueText(val)
	case nil:
		return changeset.NewValueNull()
	default:
		panic(fmt.Sprintf("unexpected type %T", v))
	}
}

// --- Test: InvertChangeset ---

func TestInvertChangeset_InsertDelete(t *testing.T) {
	tmpDir := t.TempDir()

	// Use consistent 2-column schema for all entries on the same table
	table2Col := &changeset.ChangesetTable{
		Name:        "test",
		PrimaryKeys: []bool{true, false},
	}

	// INSERT (pk=1, val="a")
	insertEntry := changeset.ChangesetEntry{
		Op:    changeset.OpInsert,
		Table: table2Col,
		NewValues: []changeset.Value{
			changeset.NewValueInt(1),
			changeset.NewValueText("a"),
		},
	}
	// DELETE (pk=2, val="b")
	deleteEntry := changeset.ChangesetEntry{
		Op:    changeset.OpDelete,
		Table: table2Col,
		OldValues: []changeset.Value{
			changeset.NewValueInt(2),
			changeset.NewValueText("b"),
		},
	}
	// UPDATE (pk=3, old="old", new="new")
	updateEntry := changeset.ChangesetEntry{
		Op:    changeset.OpUpdate,
		Table: table2Col,
		OldValues: []changeset.Value{
			changeset.NewValueInt(3),
			changeset.NewValueText("old"),
		},
		NewValues: []changeset.Value{
			changeset.NewValueUndefined(),
			changeset.NewValueText("new"),
		},
	}

	input := filepath.Join(tmpDir, "input.diff")
	if err := writeChangeset(input, []changeset.ChangesetEntry{
		insertEntry, deleteEntry, updateEntry,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	output := filepath.Join(tmpDir, "output.diff")
	if err := InvertChangeset(input, output); err != nil {
		t.Fatalf("InvertChangeset: %v", err)
	}

	inverted, err := readEntries(output)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(inverted) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(inverted))
	}

	// INSERT 1 → DELETE 1
	if inverted[0].Op != changeset.OpDelete {
		t.Errorf("entry 0: expected DELETE, got %s", inverted[0].Op)
	}
	if pk := inverted[0].OldValues[0].AsInt(); pk != 1 {
		t.Errorf("entry 0: expected pk=1, got %d", pk)
	}

	// DELETE 2 → INSERT 2
	if inverted[1].Op != changeset.OpInsert {
		t.Errorf("entry 1: expected INSERT, got %s", inverted[1].Op)
	}
	if pk := inverted[1].NewValues[0].AsInt(); pk != 2 {
		t.Errorf("entry 1: expected pk=2, got %d", pk)
	}

	// UPDATE 3: old↔new swapped
	if inverted[2].Op != changeset.OpUpdate {
		t.Errorf("entry 2: expected UPDATE, got %s", inverted[2].Op)
	}
	if old := inverted[2].OldValues[1].AsText(); old != "new" {
		t.Errorf("entry 2: expected old='new', got %q", old)
	}
	if newVal := inverted[2].NewValues[1].AsText(); newVal != "old" {
		t.Errorf("entry 2: expected new='old', got %q", newVal)
	}
}

// --- Test: RebaseDirect with real GPKG files ---

func TestRebaseDirect_GpkgNoConflict(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "base.gpkg")
	if _, err := os.Stat(baseFile); os.IsNotExist(err) {
		t.Skipf("skipping: base.gpkg not found")
	}

	tmpDir := t.TempDir()
	oursFile := filepath.Join(tmpDir, "ours.gpkg")
	theirsFile := filepath.Join(tmpDir, "theirs.gpkg")
	conflictFile := filepath.Join(tmpDir, "conflicts.json")

	copyFileFunc := func(src, dst string) error {
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0644)
	}

	if err := copyFileFunc(baseFile, oursFile); err != nil {
		t.Skipf("skipping: cannot copy base: %v", err)
	}
	if err := copyFileFunc(baseFile, theirsFile); err != nil {
		t.Skipf("skipping: cannot copy base: %v", err)
	}
	defer os.Remove(oursFile)
	defer os.Remove(theirsFile)

	// Rebase identical files — should be no-op
	if err := RebaseDirect(baseFile, theirsFile, oursFile, conflictFile); err != nil {
		t.Fatalf("RebaseDirect failed: %v", err)
	}

	// Conflict file should NOT exist
	if _, err := os.Stat(conflictFile); err == nil {
		t.Error("expected no conflict file for identical files")
	}
}

// --- Test: multiple tables in changeset ---

func TestRebase_MultipleTables(t *testing.T) {
	tmpDir := t.TempDir()

	// Table "a": theirs inserts PK=1, ours inserts PK=2
	// Table "b": theirs inserts PK=10, ours inserts PK=10 (conflict → remap)
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeInsertEntry("a", 1),
		makeInsertEntry("b", 10),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeInsertEntry("a", 2),
		makeInsertEntry("b", 10),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	_ = conflicts

	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}

	// We expect 2 INSERT entries: table "a" PK=2, table "b" PK=11 (remapped from 10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	entryMap := make(map[string]int)
	for _, e := range entries {
		if e.Op != changeset.OpInsert {
			t.Errorf("expected INSERT, got %s", e.Op)
			continue
		}
		entryMap[e.Table.Name] = int(e.NewValues[0].AsInt())
	}

	if pk, ok := entryMap["a"]; !ok {
		t.Error("missing entry for table 'a'")
	} else if pk != 2 {
		t.Errorf("table 'a': expected pk=2, got %d", pk)
	}

	if pk, ok := entryMap["b"]; !ok {
		t.Error("missing entry for table 'b'")
	} else if pk != 11 {
		t.Errorf("table 'b': expected pk=11 (remapped), got %d", pk)
	}
}

// --- Test: concurrent delete + delete (both delete same row) ---

func TestRebase_ConcurrentDeleteDelete(t *testing.T) {
	tmpDir := t.TempDir()

	// Both theirs and ours delete PK=5
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeDeleteEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeDeleteEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	if len(conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(conflicts))
	}

	// The delete should be skipped (both deleted, PK remapped to INVALID_FID)
	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (both deleted), got %d", len(entries))
	}
}

// --- Test: insert mapping cascade (4,5,6 → 4→6, 5→7, 6→8) ---

func TestRebase_InsertMappingCascade(t *testing.T) {
	tmpDir := t.TempDir()

	// Theirs inserts PK=4,5
	baseTheirs := filepath.Join(tmpDir, "base_theirs.diff")
	if err := writeChangeset(baseTheirs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 4),
		makeInsertEntry("test", 5),
	}); err != nil {
		t.Fatalf("write baseTheirs: %v", err)
	}

	// Ours inserts PK=4,5,6 — PK 4 and 5 conflict with theirs, need remapping
	baseOurs := filepath.Join(tmpDir, "base_ours.diff")
	if err := writeChangeset(baseOurs, []changeset.ChangesetEntry{
		makeInsertEntry("test", 4),
		makeInsertEntry("test", 5),
		makeInsertEntry("test", 6),
	}); err != nil {
		t.Fatalf("write baseOurs: %v", err)
	}

	theirsMerged := filepath.Join(tmpDir, "theirs_merged.diff")
	conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
	if err != nil {
		t.Fatalf("Rebase failed: %v", err)
	}

	_ = conflicts

	entries, err := readEntries(theirsMerged)
	if err != nil {
		t.Fatalf("read entries: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Collect PKs
	pks := make(map[int]bool)
	for _, e := range entries {
		pks[int(e.NewValues[0].AsInt())] = true
	}

	// The cascade: max theirs = 5, so freeIdx starts at 6.
	// The old 4 conflicts with theirs, mapped to 6.
	// The old 5 conflicts with theirs, mapped to 7.
	// The old 6 now conflicts with the mapped 6 (since 4→6), remapped to 8.
	t.Logf("PKs in output: %v", pks)

	if !pks[6] || !pks[7] || !pks[8] {
		t.Errorf("expected PKs {6,7,8}, got %v", mapKeys(pks))
	}
}

func mapKeys(m map[int]bool) []int {
	var keys []int
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
