/*
 GEODIFF - MIT License
 Copyright (C) 2020 Martin Dobias

 Ported to Go: reader tests
*/

package changeset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReaderEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.changeset")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	r, err := NewReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if !r.IsEmpty() {
		t.Error("expected empty changeset")
	}

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Error("expected nil entry for empty changeset")
	}
}

func TestReaderRewind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rewind.changeset")

	// Write a changeset with one table and two entries
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}

	table := ChangesetTable{
		Name:        "test_table",
		PrimaryKeys: []bool{true, false},
	}

	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(1), NewValueText("a")},
	})
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(2), NewValueText("b")},
	})
	w.Close()

	// Read once
	r, err := NewReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if r.IsEmpty() {
		t.Error("expected non-empty changeset")
	}

	e1, _ := r.NextEntry()
	e2, _ := r.NextEntry()
	e3, _ := r.NextEntry()

	if e1 == nil || e2 == nil {
		t.Fatal("expected two entries")
	}
	if e3 != nil {
		t.Fatal("expected EOF after two entries")
	}
	if e1.NewValues[0].AsInt() != 1 {
		t.Errorf("expected 1, got %d", e1.NewValues[0].AsInt())
	}

	// Rewind and read again
	r.Rewind()

	e1, _ = r.NextEntry()
	e2, _ = r.NextEntry()
	e3, _ = r.NextEntry()

	if e1 == nil || e2 == nil {
		t.Fatal("expected two entries after rewind")
	}
	if e3 != nil {
		t.Fatal("expected EOF after two entries on rewind")
	}
	if e1.NewValues[0].AsInt() != 1 {
		t.Errorf("after rewind: expected 1, got %d", e1.NewValues[0].AsInt())
	}
}

func TestReaderInsert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "insert.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "points",
		PrimaryKeys: []bool{true, false, false},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(42),
			NewValueDouble(3.14),
			NewValueText("hello"),
		},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpInsert {
		t.Errorf("expected INSERT, got %v", entry.Op)
	}
	if entry.Table.Name != "points" {
		t.Errorf("expected table 'points', got %q", entry.Table.Name)
	}
	if entry.Table.ColumnCount() != 3 {
		t.Errorf("expected 3 columns, got %d", entry.Table.ColumnCount())
	}
	if !entry.Table.PrimaryKeys[0] || entry.Table.PrimaryKeys[1] || entry.Table.PrimaryKeys[2] {
		t.Error("expected only column 0 as PK")
	}

	if len(entry.OldValues) != 0 {
		t.Error("expected no old values for INSERT")
	}
	if entry.NewValues[0].AsInt() != 42 {
		t.Errorf("expected 42, got %d", entry.NewValues[0].AsInt())
	}
	if entry.NewValues[1].AsDouble() != 3.14 {
		t.Errorf("expected 3.14, got %g", entry.NewValues[1].AsDouble())
	}
	if entry.NewValues[2].AsText() != "hello" {
		t.Errorf("expected 'hello', got %q", entry.NewValues[2].AsText())
	}

	// Verify EOF
	eofEntry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if eofEntry != nil {
		t.Error("expected EOF")
	}
}

func TestReaderDelete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delete.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpDelete,
		OldValues: []Value{NewValueInt(99)},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpDelete {
		t.Errorf("expected DELETE, got %v", entry.Op)
	}
	if entry.OldValues[0].AsInt() != 99 {
		t.Errorf("expected 99, got %d", entry.OldValues[0].AsInt())
	}
	if len(entry.NewValues) != 0 {
		t.Error("expected no new values for DELETE")
	}
}

func TestReaderUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op: OpUpdate,
		OldValues: []Value{
			NewValueInt(1),
			NewValueText("old"),
		},
		NewValues: []Value{
			NewValueUndefined(), // PK unchanged
			NewValueText("new"), // value changed
		},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpUpdate {
		t.Errorf("expected UPDATE, got %v", entry.Op)
	}
	if entry.OldValues[0].AsInt() != 1 {
		t.Errorf("expected old int 1, got %d", entry.OldValues[0].AsInt())
	}
	if entry.OldValues[1].AsText() != "old" {
		t.Errorf("expected old text 'old', got %q", entry.OldValues[1].AsText())
	}
	if !entry.NewValues[0].IsUndefined() {
		t.Error("expected undefined for PK in new values")
	}
	if entry.NewValues[1].AsText() != "new" {
		t.Errorf("expected new text 'new', got %q", entry.NewValues[1].AsText())
	}
}

func TestReaderAllValueTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alltypes.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "types",
		PrimaryKeys: []bool{true, false, false, false, false, false},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(-1234567890123456789),
			NewValueDouble(1.7976931348623157e+308),
			NewValueText("unicode: 你好世界 🌍"),
			NewValueBlob([]byte{0x00, 0xFF, 0x42, 0x01, 0x7F, 0x80}),
			NewValueNull(),
			NewValueUndefined(),
		},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	entry, err := r.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected an entry")
	}

	// Int
	if entry.NewValues[0].Type() != TypeInt {
		t.Errorf("expected TypeInt, got %v", entry.NewValues[0].Type())
	}
	if entry.NewValues[0].AsInt() != -1234567890123456789 {
		t.Errorf("int mismatch: got %d", entry.NewValues[0].AsInt())
	}

	// Double
	if entry.NewValues[1].Type() != TypeDouble {
		t.Errorf("expected TypeDouble, got %v", entry.NewValues[1].Type())
	}
	if entry.NewValues[1].AsDouble() != 1.7976931348623157e+308 {
		t.Errorf("double mismatch: got %g", entry.NewValues[1].AsDouble())
	}

	// Text
	if entry.NewValues[2].Type() != TypeText {
		t.Errorf("expected TypeText, got %v", entry.NewValues[2].Type())
	}
	if entry.NewValues[2].AsText() != "unicode: 你好世界 🌍" {
		t.Errorf("text mismatch: got %q", entry.NewValues[2].AsText())
	}

	// Blob
	if entry.NewValues[3].Type() != TypeBlob {
		t.Errorf("expected TypeBlob, got %v", entry.NewValues[3].Type())
	}
	expectedBlob := []byte{0x00, 0xFF, 0x42, 0x01, 0x7F, 0x80}
	gotBlob := entry.NewValues[3].AsBlob()
	if len(gotBlob) != len(expectedBlob) {
		t.Errorf("blob length mismatch: got %d, want %d", len(gotBlob), len(expectedBlob))
	}
	for i := range expectedBlob {
		if gotBlob[i] != expectedBlob[i] {
			t.Errorf("blob byte %d: got %02x, want %02x", i, gotBlob[i], expectedBlob[i])
		}
	}

	// Null
	if entry.NewValues[4].Type() != TypeNull {
		t.Errorf("expected TypeNull, got %v", entry.NewValues[4].Type())
	}
	if !entry.NewValues[4].IsNull() {
		t.Error("expected IsNull true")
	}

	// Undefined
	if entry.NewValues[5].Type() != TypeUndefined {
		t.Errorf("expected TypeUndefined, got %v", entry.NewValues[5].Type())
	}
	if !entry.NewValues[5].IsUndefined() {
		t.Error("expected IsUndefined true")
	}
}

func TestReaderMultipleTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multitable.changeset")

	w, _ := NewWriter(path)

	table1 := ChangesetTable{
		Name:        "foo",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table1)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(1)},
	})
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(2)},
	})

	table2 := ChangesetTable{
		Name:        "bar",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table2)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(3), NewValueText("x")},
	})

	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	// Entry 1: table "foo"
	e1, _ := r.NextEntry()
	if e1 == nil {
		t.Fatal("expected entry 1")
	}
	if e1.Table.Name != "foo" {
		t.Errorf("expected table foo, got %q", e1.Table.Name)
	}
	if e1.NewValues[0].AsInt() != 1 {
		t.Errorf("expected 1, got %d", e1.NewValues[0].AsInt())
	}

	// Entry 2: table "foo"
	e2, _ := r.NextEntry()
	if e2 == nil {
		t.Fatal("expected entry 2")
	}
	if e2.Table.Name != "foo" {
		t.Errorf("expected table foo, got %q", e2.Table.Name)
	}
	if e2.NewValues[0].AsInt() != 2 {
		t.Errorf("expected 2, got %d", e2.NewValues[0].AsInt())
	}

	// Entry 3: table "bar"
	e3, _ := r.NextEntry()
	if e3 == nil {
		t.Fatal("expected entry 3")
	}
	if e3.Table.Name != "bar" {
		t.Errorf("expected table bar, got %q", e3.Table.Name)
	}
	if e3.Table.ColumnCount() != 2 {
		t.Errorf("expected 2 columns, got %d", e3.Table.ColumnCount())
	}
	if e3.NewValues[0].AsInt() != 3 {
		t.Errorf("expected 3, got %d", e3.NewValues[0].AsInt())
	}
	if e3.NewValues[1].AsText() != "x" {
		t.Errorf("expected 'x', got %q", e3.NewValues[1].AsText())
	}

	// EOF
	e4, _ := r.NextEntry()
	if e4 != nil {
		t.Error("expected EOF")
	}
}

func TestReaderMixedOperations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1)}})
	w.WriteEntry(ChangesetEntry{Op: OpUpdate, OldValues: []Value{NewValueInt(2)}, NewValues: []Value{NewValueInt(3)}})
	w.WriteEntry(ChangesetEntry{Op: OpDelete, OldValues: []Value{NewValueInt(4)}})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	e1, _ := r.NextEntry()
	if e1.Op != OpInsert {
		t.Errorf("expected INSERT, got %v", e1.Op)
	}

	e2, _ := r.NextEntry()
	if e2.Op != OpUpdate {
		t.Errorf("expected UPDATE, got %v", e2.Op)
	}

	e3, _ := r.NextEntry()
	if e3.Op != OpDelete {
		t.Errorf("expected DELETE, got %v", e3.Op)
	}

	e4, _ := r.NextEntry()
	if e4 != nil {
		t.Error("expected EOF")
	}
}

func TestReaderVarintEdgeCases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "varint.changeset")

	w, _ := NewWriter(path)

	// Test with various column counts to exercise varint encoding edge cases
	// 1 column: varint encodes as 1 byte
	table1 := ChangesetTable{
		Name:        "t1",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table1)
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1)}})

	// 200 columns: varint encodes as 2 bytes (0x80-0x3fff range)
	pks200 := make([]bool, 200)
	pks200[0] = true
	vals200 := make([]Value, 200)
	for i := range vals200 {
		vals200[i] = NewValueInt(int64(i))
	}
	table200 := ChangesetTable{Name: "t200", PrimaryKeys: pks200}
	w.BeginTable(table200)
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: vals200})

	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	// Read table t1
	e1, _ := r.NextEntry()
	if e1.Table.Name != "t1" {
		t.Errorf("expected t1, got %q", e1.Table.Name)
	}
	if e1.Table.ColumnCount() != 1 {
		t.Errorf("expected 1 column, got %d", e1.Table.ColumnCount())
	}

	// Read table t200
	e2, _ := r.NextEntry()
	if e2.Table.Name != "t200" {
		t.Errorf("expected t200, got %q", e2.Table.Name)
	}
	if e2.Table.ColumnCount() != 200 {
		t.Errorf("expected 200 columns, got %d", e2.Table.ColumnCount())
	}
	if e2.NewValues[199].AsInt() != 199 {
		t.Errorf("expected 199, got %d", e2.NewValues[199].AsInt())
	}
}

func TestReaderLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "big",
		PrimaryKeys: []bool{true},
	}

	const numEntries = 1000
	w.BeginTable(table)
	for i := 0; i < numEntries; i++ {
		w.WriteEntry(ChangesetEntry{
			Op:        OpInsert,
			NewValues: []Value{NewValueInt(int64(i))},
		})
	}
	w.Close()

	r, _ := NewReader(path)
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
		if entry.NewValues[0].AsInt() != int64(count) {
			t.Errorf("entry %d: expected %d, got %d", count, count, entry.NewValues[0].AsInt())
		}
		count++
	}

	if count != numEntries {
		t.Errorf("expected %d entries, got %d", numEntries, count)
	}
}

func TestReaderCorruptFile(t *testing.T) {
	// Manually construct a corrupt changeset and verify error handling
	path := filepath.Join(t.TempDir(), "corrupt.changeset")

	// Write a file with a 'T' header but truncated data
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte{'T'})   // table marker
	f.Write([]byte{0x01})  // varint: 1 column
	f.Write([]byte{0x01})  // PK flag
	f.Write([]byte("tbl")) // table name (no null terminator!)
	f.Close()

	r, _ := NewReader(path)
	defer r.Close()

	_, err = r.NextEntry()
	if err == nil {
		t.Error("expected error for corrupt file")
	}
}

func TestReaderInvalidOpcode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.changeset")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Write a valid table header, then an invalid opcode
	f.Write([]byte{'T'})
	f.Write([]byte{0x01})
	f.Write([]byte{0x01})
	f.Write([]byte("tbl"))
	f.Write([]byte{0x00})
	f.Write([]byte{0xFF}) // invalid opcode
	f.Close()

	r, _ := NewReader(path)
	defer r.Close()

	_, err = r.NextEntry()
	if err == nil {
		t.Error("expected error for invalid opcode")
	}
}
