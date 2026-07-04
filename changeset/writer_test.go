/*
 GEODIFF - MIT License
 Copyright (C) 2020 Martin Dobias

 Ported to Go: writer tests + round-trip tests
*/

package changeset

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

// --- Round-trip tests (write then read back and verify) ---

func TestRoundTripInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_insert.changeset")

	// Write
	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "points",
		PrimaryKeys: []bool{true, false, false},
	}
	w.BeginTable(table)
	original := ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(42),
			NewValueDouble(3.141592653589793),
			NewValueText("hello, world"),
		},
	}
	w.WriteEntry(original)
	w.Close()

	// Read back
	r, _ := NewReader(path)
	defer r.Close()

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected an entry")
	}

	// Verify op
	if entry.Op != OpInsert {
		t.Errorf("op mismatch: got %v, want INSERT", entry.Op)
	}

	// Verify table name
	if entry.Table.Name != "points" {
		t.Errorf("table name mismatch: got %q, want 'points'", entry.Table.Name)
	}

	// Verify column count
	if entry.Table.ColumnCount() != 3 {
		t.Errorf("column count: got %d, want 3", entry.Table.ColumnCount())
	}

	// Verify PK flags
	for i, pk := range entry.Table.PrimaryKeys {
		expected := (i == 0)
		if pk != expected {
			t.Errorf("PK[%d]: got %v, want %v", i, pk, expected)
		}
	}

	// Verify values
	if len(entry.NewValues) != 3 {
		t.Fatalf("new values length: got %d, want 3", len(entry.NewValues))
	}
	if !entry.NewValues[0].Equal(original.NewValues[0]) {
		t.Errorf("newValues[0] mismatch: got %v, want %v", entry.NewValues[0], original.NewValues[0])
	}
	if !entry.NewValues[1].Equal(original.NewValues[1]) {
		t.Errorf("newValues[1] mismatch: got %v, want %v", entry.NewValues[1], original.NewValues[1])
	}
	if !entry.NewValues[2].Equal(original.NewValues[2]) {
		t.Errorf("newValues[2] mismatch: got %v, want %v", entry.NewValues[2], original.NewValues[2])
	}

	// Verify no old values for INSERT
	if len(entry.OldValues) != 0 {
		t.Errorf("old values length: got %d, want 0", len(entry.OldValues))
	}
}

func TestRoundTripDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_delete.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	original := ChangesetEntry{
		Op:        OpDelete,
		OldValues: []Value{NewValueInt(99)},
	}
	w.WriteEntry(original)
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpDelete {
		t.Errorf("op mismatch: got %v, want DELETE", entry.Op)
	}
	if len(entry.NewValues) != 0 {
		t.Error("expected no new values for DELETE")
	}
	if !entry.OldValues[0].Equal(original.OldValues[0]) {
		t.Errorf("oldValues[0] mismatch")
	}
}

func TestRoundTripUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_update.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table)
	original := ChangesetEntry{
		Op: OpUpdate,
		OldValues: []Value{
			NewValueInt(1),
			NewValueText("old"),
		},
		NewValues: []Value{
			NewValueUndefined(),
			NewValueText("new"),
		},
	}
	w.WriteEntry(original)
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpUpdate {
		t.Errorf("op mismatch: got %v, want UPDATE", entry.Op)
	}

	// Verify old values
	if !entry.OldValues[0].Equal(original.OldValues[0]) {
		t.Errorf("oldValues[0] mismatch")
	}
	if !entry.OldValues[1].Equal(original.OldValues[1]) {
		t.Errorf("oldValues[1] mismatch")
	}

	// Verify new values
	if !entry.NewValues[0].IsUndefined() {
		t.Error("expected undefined for PK in new values")
	}
	if !entry.NewValues[1].Equal(original.NewValues[1]) {
		t.Errorf("newValues[1] mismatch")
	}
}

func TestRoundTripAllValueTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_alltypes.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "types",
		PrimaryKeys: []bool{true, false, false, false, false, false},
	}
	w.BeginTable(table)

	original := ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(0),                              // zero
			NewValueInt(-1),                             // negative
			NewValueInt(math.MaxInt64),                  // max int64
			NewValueInt(math.MinInt64),                  // min int64
			NewValueDouble(0.0),                         // zero double
			NewValueDouble(-1.5),                        // negative double
			NewValueDouble(math.MaxFloat64),             // max float64
			NewValueDouble(math.SmallestNonzeroFloat64), // min positive
			NewValueText(""),                            // empty string
			NewValueText("a"),                           // single char
			NewValueText("hello world"),                 // normal string
			NewValueText("日本語"),                         // Unicode
			NewValueBlob([]byte{}),                      // empty blob
			NewValueBlob([]byte{0x00}),                  // blob with zero
			NewValueBlob([]byte{0xFF, 0xFE, 0xFD}),      // blob
			NewValueNull(),                              // null
			NewValueUndefined(),                         // undefined
		},
	}
	// We need exactly 17 columns in the table
	// Wait, we have 17 values but table was defined with 6!
	// Redefine table to match
	w.Close()
	os.Remove(path)

	w, _ = NewWriter(path)
	table = ChangesetTable{
		Name:        "types",
		PrimaryKeys: make([]bool, len(original.NewValues)),
	}
	table.PrimaryKeys[0] = true // first column is PK
	w.BeginTable(table)
	w.WriteEntry(original)
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpInsert {
		t.Errorf("op mismatch: got %v, want INSERT", entry.Op)
	}
	if len(entry.NewValues) != len(original.NewValues) {
		t.Fatalf("value count mismatch: got %d, want %d", len(entry.NewValues), len(original.NewValues))
	}

	for i := range original.NewValues {
		if !entry.NewValues[i].Equal(original.NewValues[i]) {
			t.Errorf("values[%d] mismatch:\n  got  %v\n  want %v", i, entry.NewValues[i], original.NewValues[i])
		}
	}
}

func TestRoundTripMultipleTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_multitable.changeset")

	w, _ := NewWriter(path)

	table1 := ChangesetTable{
		Name:        "foo",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table1)
	w.WriteEntry(ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(1),
			NewValueText("one"),
		},
	})
	w.WriteEntry(ChangesetEntry{
		Op: OpUpdate,
		OldValues: []Value{
			NewValueInt(2),
			NewValueText("old"),
		},
		NewValues: []Value{
			NewValueUndefined(),
			NewValueText("new"),
		},
	})

	table2 := ChangesetTable{
		Name:        "bar",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table2)
	w.WriteEntry(ChangesetEntry{
		Op:        OpDelete,
		OldValues: []Value{NewValueInt(999)},
	})

	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	// Entry 1: INSERT into foo
	e1, _ := r.NextEntry()
	if e1.Op != OpInsert {
		t.Errorf("e1: expected INSERT, got %v", e1.Op)
	}
	if e1.Table.Name != "foo" {
		t.Errorf("e1: expected table foo, got %q", e1.Table.Name)
	}
	if e1.NewValues[0].AsInt() != 1 {
		t.Errorf("e1: expected int 1")
	}
	if e1.NewValues[1].AsText() != "one" {
		t.Errorf("e1: expected text 'one'")
	}

	// Entry 2: UPDATE on foo
	e2, _ := r.NextEntry()
	if e2.Op != OpUpdate {
		t.Errorf("e2: expected UPDATE, got %v", e2.Op)
	}
	if e2.Table.Name != "foo" {
		t.Errorf("e2: expected table foo, got %q", e2.Table.Name)
	}

	// Entry 3: DELETE from bar
	e3, _ := r.NextEntry()
	if e3.Op != OpDelete {
		t.Errorf("e3: expected DELETE, got %v", e3.Op)
	}
	if e3.Table.Name != "bar" {
		t.Errorf("e3: expected table bar, got %q", e3.Table.Name)
	}

	// EOF
	e4, _ := r.NextEntry()
	if e4 != nil {
		t.Error("expected EOF")
	}
}

func TestRoundTripMultipleEntriesSameTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_manyentries.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "data",
		PrimaryKeys: []bool{true, false},
	}

	w.BeginTable(table)
	entries := []ChangesetEntry{
		{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("a")}},
		{Op: OpInsert, NewValues: []Value{NewValueInt(2), NewValueText("b")}},
		{Op: OpInsert, NewValues: []Value{NewValueInt(3), NewValueText("c")}},
		{Op: OpUpdate, OldValues: []Value{NewValueInt(1), NewValueText("a")}, NewValues: []Value{NewValueUndefined(), NewValueText("A")}},
		{Op: OpDelete, OldValues: []Value{NewValueInt(2), NewValueText("b")}},
	}

	for _, e := range entries {
		w.WriteEntry(e)
	}
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	for i, expected := range entries {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatalf("entry %d: unexpected error: %v", i, err)
		}
		if entry == nil {
			t.Fatalf("entry %d: unexpected nil", i)
		}
		if entry.Op != expected.Op {
			t.Errorf("entry %d: op mismatch: got %v, want %v", i, entry.Op, expected.Op)
		}
		if entry.Table.Name != "data" {
			t.Errorf("entry %d: table mismatch: got %q, want 'data'", i, entry.Table.Name)
		}
	}

	// EOF
	eof, _ := r.NextEntry()
	if eof != nil {
		t.Error("expected EOF")
	}
}

func TestRoundTripEmptyTable(t *testing.T) {
	// A table with zero columns
	path := filepath.Join(t.TempDir(), "rt_emptycol.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "empty",
		PrimaryKeys: []bool{},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	if entry.Table.Name != "empty" {
		t.Errorf("expected table 'empty', got %q", entry.Table.Name)
	}
	if entry.Table.ColumnCount() != 0 {
		t.Errorf("expected 0 columns, got %d", entry.Table.ColumnCount())
	}
	if len(entry.NewValues) != 0 {
		t.Errorf("expected 0 values, got %d", len(entry.NewValues))
	}
}

func TestRoundTripVarintEdgeCases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt_varint.changeset")

	w, _ := NewWriter(path)

	// Column count of 0: 1-byte varint (0x00)
	table0 := ChangesetTable{
		Name:        "t0",
		PrimaryKeys: []bool{},
	}
	w.BeginTable(table0)
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{}})

	// Column count of 127: 1-byte varint max (0x7F)
	pks127 := make([]bool, 127)
	vals127 := make([]Value, 127)
	for i := range vals127 {
		vals127[i] = NewValueNull()
	}
	table127 := ChangesetTable{Name: "t127", PrimaryKeys: pks127}
	w.BeginTable(table127)
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: vals127})

	// Column count of 128: 2-byte varint (0x81 0x00)
	pks128 := make([]bool, 128)
	vals128 := make([]Value, 128)
	for i := range vals128 {
		vals128[i] = NewValueNull()
	}
	table128 := ChangesetTable{Name: "t128", PrimaryKeys: pks128}
	w.BeginTable(table128)
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: vals128})

	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	// Read t0
	e0, _ := r.NextEntry()
	if e0.Table.ColumnCount() != 0 {
		t.Errorf("t0: expected 0 columns, got %d", e0.Table.ColumnCount())
	}

	// Read t127
	e127, _ := r.NextEntry()
	if e127.Table.ColumnCount() != 127 {
		t.Errorf("t127: expected 127 columns, got %d", e127.Table.ColumnCount())
	}

	// Read t128
	e128, _ := r.NextEntry()
	if e128.Table.ColumnCount() != 128 {
		t.Errorf("t128: expected 128 columns, got %d", e128.Table.ColumnCount())
	}
}

func TestRoundTripTableName(t *testing.T) {
	// Test various table names
	path := filepath.Join(t.TempDir(), "rt_tablenames.changeset")

	w, _ := NewWriter(path)

	names := []string{
		"",             // empty name
		"t",            // single char
		"simple_table", // normal
		"UPPERCASE",    // all caps
		"table_123",    // with numbers
		"with-dash",    // with dash
		"with space",   // with space
	}

	for _, name := range names {
		table := ChangesetTable{
			Name:        name,
			PrimaryKeys: []bool{true},
		}
		w.BeginTable(table)
		w.WriteEntry(ChangesetEntry{
			Op:        OpInsert,
			NewValues: []Value{NewValueInt(1)},
		})
	}
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	for _, expectedName := range names {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatalf("unexpected error for table %q: %v", expectedName, err)
		}
		if entry == nil {
			t.Fatalf("unexpected nil for table %q", expectedName)
		}
		if entry.Table.Name != expectedName {
			t.Errorf("table name mismatch: got %q, want %q", entry.Table.Name, expectedName)
		}
	}

	eof, _ := r.NextEntry()
	if eof != nil {
		t.Error("expected EOF")
	}
}

func TestWriterEmptyTableName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "emptyname.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(1), NewValueText("x")},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry.Table.Name != "" {
		t.Errorf("expected empty table name, got %q", entry.Table.Name)
	}
	if entry.Table.ColumnCount() != 2 {
		t.Errorf("expected 2 columns, got %d", entry.Table.ColumnCount())
	}
}

func TestWriterBigBlob(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bigblob.changeset")

	// Create a blob with 10000 bytes
	bigData := make([]byte, 10000)
	for i := range bigData {
		bigData[i] = byte(i % 256)
	}

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "data",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueBlob(bigData)},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	gotBlob := entry.NewValues[0].AsBlob()
	if len(gotBlob) != 10000 {
		t.Errorf("blob length: got %d, want 10000", len(gotBlob))
	}
	for i := range bigData {
		if gotBlob[i] != bigData[i] {
			t.Errorf("blob byte %d: got %02x, want %02x", i, gotBlob[i], bigData[i])
			break
		}
	}
}

func TestWriterNewFileOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overwrite.changeset")

	// First write
	w1, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1)}})
	w1.Close()

	// Overwrite with different content
	w2, _ := NewWriter(path)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(999)}})
	w2.Close()

	// Read back - should see only the second write
	r, _ := NewReader(path)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	if entry.NewValues[0].AsInt() != 999 {
		t.Errorf("expected 999, got %d", entry.NewValues[0].AsInt())
	}

	eof, _ := r.NextEntry()
	if eof != nil {
		t.Error("expected only one entry")
	}
}
