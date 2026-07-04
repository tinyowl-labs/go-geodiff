/*
 GEODIFF - MIT License
 Copyright (C) 2020 Martin Dobias

 Ported to Go: utils + concat tests
*/

package changeset

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// ----- Hex helpers -----

func TestHexToBinRoundTrip(t *testing.T) {
	original := []byte{0x00, 0xFF, 0x42, 0x01, 0x7F, 0x80, 0xDE, 0xAD, 0xBE, 0xEF}

	hexStr := BinToHex(original)
	decoded, err := HexToBin(hexStr)
	if err != nil {
		t.Fatalf("HexToBin failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("byte %d: got %02x, want %02x", i, decoded[i], original[i])
		}
	}
}

func TestHexToBinEmpty(t *testing.T) {
	decoded, err := HexToBin("")
	if err != nil {
		t.Fatalf("HexToBin empty string failed: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("expected 0 bytes, got %d", len(decoded))
	}
}

func TestBinToHexEmpty(t *testing.T) {
	result := BinToHex([]byte{})
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestHexToBinInvalid(t *testing.T) {
	_, err := HexToBin("ZZZ")
	if err == nil {
		t.Error("expected error for invalid hex")
	}
}

// ----- Invert -----

func TestInvertInsertBecomesDelete(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "invert_insert_in.changeset")
	outputPath := filepath.Join(t.TempDir(), "invert_insert_out.changeset")

	// Write an INSERT
	w, _ := NewWriter(inputPath)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(1),
			NewValueText("hello"),
		},
	})
	w.Close()

	// Invert
	r, _ := NewReader(inputPath)
	w2, _ := NewWriter(outputPath)
	if err := InvertChangeset(r, w2); err != nil {
		t.Fatal(err)
	}
	r.Close()
	w2.Close()

	// Read back: should be DELETE with oldValues = [1, "hello"]
	rr, _ := NewReader(outputPath)
	defer rr.Close()

	entry, err := rr.NextEntry()
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpDelete {
		t.Errorf("expected OpDelete, got %v", entry.Op)
	}
	if entry.Table.Name != "t" {
		t.Errorf("expected table 't', got %q", entry.Table.Name)
	}
	if len(entry.OldValues) != 2 {
		t.Fatalf("expected 2 old values, got %d", len(entry.OldValues))
	}
	if entry.OldValues[0].AsInt() != 1 {
		t.Errorf("expected old int 1, got %d", entry.OldValues[0].AsInt())
	}
	if entry.OldValues[1].AsText() != "hello" {
		t.Errorf("expected old text 'hello', got %q", entry.OldValues[1].AsText())
	}
	if len(entry.NewValues) != 0 {
		t.Errorf("expected no new values for DELETE, got %d", len(entry.NewValues))
	}

	// Verify EOF
	eof, _ := rr.NextEntry()
	if eof != nil {
		t.Error("expected EOF")
	}
}

func TestInvertDeleteBecomesInsert(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "invert_delete_in.changeset")
	outputPath := filepath.Join(t.TempDir(), "invert_delete_out.changeset")

	w, _ := NewWriter(inputPath)
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

	r, _ := NewReader(inputPath)
	w2, _ := NewWriter(outputPath)
	if err := InvertChangeset(r, w2); err != nil {
		t.Fatal(err)
	}
	r.Close()
	w2.Close()

	rr, _ := NewReader(outputPath)
	defer rr.Close()

	entry, _ := rr.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpInsert {
		t.Errorf("expected OpInsert, got %v", entry.Op)
	}
	if entry.NewValues[0].AsInt() != 99 {
		t.Errorf("expected new int 99, got %d", entry.NewValues[0].AsInt())
	}
}

func TestInvertUpdateSwapsOldNew(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "invert_update_in.changeset")
	outputPath := filepath.Join(t.TempDir(), "invert_update_out.changeset")

	w, _ := NewWriter(inputPath)
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
			NewValueText("new"),
		},
	})
	w.Close()

	r, _ := NewReader(inputPath)
	w2, _ := NewWriter(outputPath)
	if err := InvertChangeset(r, w2); err != nil {
		t.Fatal(err)
	}
	r.Close()
	w2.Close()

	rr, _ := NewReader(outputPath)
	defer rr.Close()

	entry, _ := rr.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}

	if entry.Op != OpUpdate {
		t.Errorf("expected OpUpdate, got %v", entry.Op)
	}

	// After swap: old = original new, new = original old
	// But PK column had new undefined originally, so after swap:
	// old[0] was new[0]=Undefined, need to check the PK reversal logic.
	// The C++ code says: if PK column in old has undefined, swap from new.
	// Original: old=[int(1), "old"], new=[undefined, "new"]
	// After swap: old=[undefined, "new"], new=[int(1), "old"]
	// Then PK reversal: old[0] is undefined → old[0] = new[0] = int(1), new[0] = undefined
	// Result: old=[int(1), "new"], new=[undefined, "old"]
	if entry.OldValues[0].AsInt() != 1 {
		t.Errorf("expected old[0] = 1, got %v", entry.OldValues[0])
	}
	if entry.OldValues[1].AsText() != "new" {
		t.Errorf("expected old[1] = 'new', got %v", entry.OldValues[1])
	}
	if !entry.NewValues[0].IsUndefined() {
		t.Errorf("expected new[0] undefined, got %v", entry.NewValues[0])
	}
	if entry.NewValues[1].AsText() != "old" {
		t.Errorf("expected new[1] = 'old', got %v", entry.NewValues[1])
	}
}

func TestInvertRoundTrip(t *testing.T) {
	// entry → invert → invert = original
	inputPath := filepath.Join(t.TempDir(), "invert_rt_in.changeset")
	invertedPath := filepath.Join(t.TempDir(), "invert_rt_mid.changeset")
	doubleInvertedPath := filepath.Join(t.TempDir(), "invert_rt_out.changeset")

	w, _ := NewWriter(inputPath)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true, false},
	}

	entries := []ChangesetEntry{
		{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("a")}},
		{Op: OpInsert, NewValues: []Value{NewValueInt(2), NewValueText("b")}},
		{
			Op:        OpUpdate,
			OldValues: []Value{NewValueInt(1), NewValueText("a")},
			NewValues: []Value{NewValueUndefined(), NewValueText("A")},
		},
		{Op: OpDelete, OldValues: []Value{NewValueInt(2), NewValueText("b")}},
	}

	w.BeginTable(table)
	for _, e := range entries {
		w.WriteEntry(e)
	}
	w.Close()

	// First invert
	r1, _ := NewReader(inputPath)
	w1, _ := NewWriter(invertedPath)
	if err := InvertChangeset(r1, w1); err != nil {
		t.Fatal(err)
	}
	r1.Close()
	w1.Close()

	// Second invert (back to original)
	r2, _ := NewReader(invertedPath)
	w2, _ := NewWriter(doubleInvertedPath)
	if err := InvertChangeset(r2, w2); err != nil {
		t.Fatal(err)
	}
	r2.Close()
	w2.Close()

	// Read both and compare
	rOrig, _ := NewReader(inputPath)
	defer rOrig.Close()
	rResult, _ := NewReader(doubleInvertedPath)
	defer rResult.Close()

	for i := 0; i < len(entries); i++ {
		orig, _ := rOrig.NextEntry()
		result, _ := rResult.NextEntry()

		if orig == nil || result == nil {
			t.Fatalf("entry %d: unexpected nil", i)
		}

		if orig.Op != result.Op {
			t.Errorf("entry %d: op mismatch: %v vs %v", i, orig.Op, result.Op)
		}

		for j := range orig.OldValues {
			if !orig.OldValues[j].Equal(result.OldValues[j]) {
				t.Errorf("entry %d, oldValues[%d]: %v vs %v", i, j, orig.OldValues[j], result.OldValues[j])
			}
		}
		for j := range orig.NewValues {
			if !orig.NewValues[j].Equal(result.NewValues[j]) {
				t.Errorf("entry %d, newValues[%d]: %v vs %v", i, j, orig.NewValues[j], result.NewValues[j])
			}
		}
	}
}

// ----- ChangesetToJSON -----

func TestChangesetToJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tojson.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "points",
		PrimaryKeys: []bool{true, false},
	}

	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(1), NewValueText("hello")},
	})
	w.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(2), NewValueText("old")},
		NewValues: []Value{NewValueUndefined(), NewValueText("new")},
	})
	w.WriteEntry(ChangesetEntry{
		Op:        OpDelete,
		OldValues: []Value{NewValueInt(3), NewValueText("bye")},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	jsonBytes, err := ChangesetToJSON(r)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries, ok := result["geodiff"].([]interface{})
	if !ok {
		t.Fatal("expected 'geodiff' array")
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Check first entry (insert)
	e0 := entries[0].(map[string]interface{})
	if e0["table"] != "points" {
		t.Errorf("expected table 'points', got %v", e0["table"])
	}
	if e0["type"] != "insert" {
		t.Errorf("expected type 'insert', got %v", e0["type"])
	}

	// Check second entry (update)
	e1 := entries[1].(map[string]interface{})
	if e1["type"] != "update" {
		t.Errorf("expected type 'update', got %v", e1["type"])
	}

	// Check third entry (delete)
	e2 := entries[2].(map[string]interface{})
	if e2["type"] != "delete" {
		t.Errorf("expected type 'delete', got %v", e2["type"])
	}
}

func TestChangesetToJSON_BlobBase64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tojson_blob.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "data",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueBlob([]byte{0x00, 0xFF, 0x42})},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	jsonBytes, err := ChangesetToJSON(r)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the blob is base64-encoded
	str := string(jsonBytes)
	if !strings.Contains(str, "AP9C") {
		// "AP9C" is the base64 of [0x00, 0xFF, 0x42]
		t.Errorf("expected base64 'AP9C' in JSON, got: %s", str)
	}
}

func TestChangesetToJSON_Null(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tojson_null.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(1),
			NewValueNull(),
		},
	})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	jsonBytes, err := ChangesetToJSON(r)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries := result["geodiff"].([]interface{})
	e0 := entries[0].(map[string]interface{})
	changes := e0["changes"].([]interface{})

	// Should have two changes: PK column and the null column
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}

	// The null column should have "new": null (Go nil → JSON null)
	c1 := changes[1].(map[string]interface{})
	if _, hasNew := c1["new"]; !hasNew {
		t.Error("expected 'new' key in change")
	}
	if c1["new"] != nil {
		t.Errorf("expected null for null value, got %v", c1["new"])
	}
}

func TestChangesetToJSON_EmptyChangeset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tojson_empty.changeset")

	w, _ := NewWriter(path)
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	jsonBytes, err := ChangesetToJSON(r)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries := result["geodiff"].([]interface{})
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

// ----- ChangesetToJSONSummary -----

func TestChangesetToJSONSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.changeset")

	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}

	w.BeginTable(table)
	// 3 inserts, 2 updates, 1 delete
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1)}})
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(2)}})
	w.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(3)}})
	w.WriteEntry(ChangesetEntry{Op: OpUpdate, OldValues: []Value{NewValueInt(1)}, NewValues: []Value{NewValueInt(10)}})
	w.WriteEntry(ChangesetEntry{Op: OpUpdate, OldValues: []Value{NewValueInt(2)}, NewValues: []Value{NewValueInt(20)}})
	w.WriteEntry(ChangesetEntry{Op: OpDelete, OldValues: []Value{NewValueInt(3)}})
	w.Close()

	r, _ := NewReader(path)
	defer r.Close()

	jsonBytes, err := ChangesetToJSONSummary(r)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries, ok := result["geodiff_summary"].([]interface{})
	if !ok {
		t.Fatal("expected 'geodiff_summary' array")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 table summary, got %d", len(entries))
	}

	summary := entries[0].(map[string]interface{})
	if summary["table"] != "t" {
		t.Errorf("expected table 't', got %v", summary["table"])
	}

	// JSON numbers are float64 by default in Go's json.Unmarshal
	inserts := summary["insert"].(float64)
	updates := summary["update"].(float64)
	deletes := summary["delete"].(float64)

	if inserts != 3 {
		t.Errorf("expected 3 inserts, got %v", inserts)
	}
	if updates != 2 {
		t.Errorf("expected 2 updates, got %v", updates)
	}
	if deletes != 1 {
		t.Errorf("expected 1 delete, got %v", deletes)
	}
}

// ----- ConflictsToJSON -----

func TestConflictsToJSON(t *testing.T) {
	conflicts := []ConflictFeature{
		{
			PK:        1,
			TableName: "points",
			Items: []ConflictItem{
				{Column: 0, Base: NewValueInt(5), Theirs: NewValueInt(6), Ours: NewValueInt(7)},
				{Column: 1, Base: NewValueText("a"), Theirs: NewValueText("b"), Ours: NewValueText("c")},
			},
		},
	}

	jsonBytes, err := ConflictsToJSON(conflicts)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entries := result["geodiff"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e0 := entries[0].(map[string]interface{})
	if e0["table"] != "points" {
		t.Errorf("expected table 'points', got %v", e0["table"])
	}
	if e0["type"] != "conflict" {
		t.Errorf("expected type 'conflict', got %v", e0["type"])
	}
	if e0["fid"] != "1" {
		t.Errorf("expected fid '1', got %v", e0["fid"])
	}

	changes := e0["changes"].([]interface{})
	if len(changes) != 2 {
		t.Fatalf("expected 2 conflict items, got %d", len(changes))
	}
}

// ----- ConcatChangesets -----

func TestConcatTwoInserts(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_out.changeset")

	// File 1: INSERT pk=1
	path1 := filepath.Join(t.TempDir(), "concat_in1.changeset")
	w1, _ := NewWriter(path1)
	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("a")}})
	w1.Close()

	// File 2: INSERT pk=2
	path2 := filepath.Join(t.TempDir(), "concat_in2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(2), NewValueText("b")}})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	// Read output: should have 2 inserts (order is non-deterministic)
	r, _ := NewReader(outPath)
	defer r.Close()

	seen := map[int64]bool{}
	for {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil {
			break
		}
		if entry.Op != OpInsert {
			t.Errorf("expected OpInsert, got %v", entry.Op)
		}
		pk := entry.NewValues[0].AsInt()
		seen[pk] = true
	}

	if !seen[1] {
		t.Error("expected pk=1")
	}
	if !seen[2] {
		t.Error("expected pk=2")
	}
	if len(seen) != 2 {
		t.Errorf("expected 2 entries, got %d", len(seen))
	}
}

func TestConcatInsertDeleteSamePK_Cancel(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_cancel.changeset")

	// File 1: INSERT pk=1
	path1 := filepath.Join(t.TempDir(), "concat_c1.changeset")
	w1, _ := NewWriter(path1)
	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true}}
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1)}})
	w1.Close()

	// File 2: DELETE pk=1
	path2 := filepath.Join(t.TempDir(), "concat_c2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{Op: OpDelete, OldValues: []Value{NewValueInt(1)}})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	// Read output: should be empty (INSERT + DELETE cancel out)
	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry != nil {
		t.Errorf("expected no entries (INSERT+DELETE canceled), got %+v", entry)
	}
}

func TestConcatUpdateUpdateSamePK_Merge(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_merge.changeset")

	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}

	// File 1: UPDATE pk=1, col1 "old"→"mid"
	path1 := filepath.Join(t.TempDir(), "concat_m1.changeset")
	w1, _ := NewWriter(path1)
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1), NewValueText("old")},
		NewValues: []Value{NewValueUndefined(), NewValueText("mid")},
	})
	w1.Close()

	// File 2: UPDATE pk=1, col1 "mid"→"new"
	path2 := filepath.Join(t.TempDir(), "concat_m2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1), NewValueText("mid")},
		NewValues: []Value{NewValueUndefined(), NewValueText("new")},
	})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	if entry.Op != OpUpdate {
		t.Errorf("expected OpUpdate, got %v", entry.Op)
	}

	// After merge: mergeValue(vOne, vTwo) returns vTwo if not undefined.
	// C++ call: mergeUpdate(t, e2.old, e1.old, e1.new, e2.new, ...)
	// vOld = mergeValue(e2.old="mid", e1.old="old") = "old" (vTwo wins)
	// vNew = mergeValue(e1.new="mid", e2.new="new") = "new" (vTwo wins)
	// So the merged UPDATE has old="old", new="new" with PK preserved.
	if entry.OldValues[0].AsInt() != 1 {
		t.Errorf("expected old[0] = 1, got %v", entry.OldValues[0])
	}
	if entry.OldValues[1].AsText() != "old" {
		t.Errorf("expected old[1] = 'old', got %v", entry.OldValues[1])
	}
	if !entry.NewValues[0].IsUndefined() {
		t.Errorf("expected new[0] undefined, got %v", entry.NewValues[0])
	}
	if entry.NewValues[1].AsText() != "new" {
		t.Errorf("expected new[1] = 'new', got %v", entry.NewValues[1])
	}

	eof, _ := r.NextEntry()
	if eof != nil {
		t.Error("expected EOF")
	}
}

func TestConcatInsertUpdateSamePK(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_ins_upd.changeset")

	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}

	// File 1: INSERT pk=1, col1="a"
	path1 := filepath.Join(t.TempDir(), "concat_iu1.changeset")
	w1, _ := NewWriter(path1)
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("a")}})
	w1.Close()

	// File 2: UPDATE pk=1, col1="a"→"b"
	path2 := filepath.Join(t.TempDir(), "concat_iu2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1), NewValueText("a")},
		NewValues: []Value{NewValueUndefined(), NewValueText("b")},
	})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	// INSERT + UPDATE → modified INSERT (column 1 updated to "b")
	if entry.Op != OpInsert {
		t.Errorf("expected OpInsert, got %v", entry.Op)
	}
	if entry.NewValues[0].AsInt() != 1 {
		t.Errorf("expected newValues[0] = 1, got %v", entry.NewValues[0])
	}
	if entry.NewValues[1].AsText() != "b" {
		t.Errorf("expected newValues[1] = 'b', got %v", entry.NewValues[1])
	}
}

func TestConcatDeleteInsertSamePK_BecomesUpdate(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_del_ins.changeset")

	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}

	// File 1: DELETE pk=1, col1="a"
	path1 := filepath.Join(t.TempDir(), "concat_di1.changeset")
	w1, _ := NewWriter(path1)
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{
		Op:        OpDelete,
		OldValues: []Value{NewValueInt(1), NewValueText("a")},
	})
	w1.Close()

	// File 2: INSERT pk=1, col1="b" (new row with same PK)
	path2 := filepath.Join(t.TempDir(), "concat_di2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("b")}})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	// DELETE + INSERT → UPDATE (old "a", new "b")
	if entry.Op != OpUpdate {
		t.Errorf("expected OpUpdate, got %v", entry.Op)
	}
	if entry.OldValues[0].AsInt() != 1 {
		t.Errorf("expected oldValues[0] = 1, got %v", entry.OldValues[0])
	}
	if entry.OldValues[1].AsText() != "a" {
		t.Errorf("expected oldValues[1] = 'a', got %v", entry.OldValues[1])
	}
	if !entry.NewValues[0].IsUndefined() {
		t.Errorf("expected newValues[0] undefined, got %v", entry.NewValues[0])
	}
	if entry.NewValues[1].AsText() != "b" {
		t.Errorf("expected newValues[1] = 'b', got %v", entry.NewValues[1])
	}
}

func TestConcatUpdateDeleteSamePK(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_upd_del.changeset")

	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}

	// File 1: UPDATE pk=1, col1="a"→"b"
	path1 := filepath.Join(t.TempDir(), "concat_ud1.changeset")
	w1, _ := NewWriter(path1)
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1), NewValueText("a")},
		NewValues: []Value{NewValueUndefined(), NewValueText("b")},
	})
	w1.Close()

	// File 2: DELETE pk=1, col1="b"
	path2 := filepath.Join(t.TempDir(), "concat_ud2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{
		Op:        OpDelete,
		OldValues: []Value{NewValueInt(1), NewValueText("b")},
	})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	// UPDATE + DELETE → DELETE with old values backfilled
	if entry.Op != OpDelete {
		t.Errorf("expected OpDelete, got %v", entry.Op)
	}
	if entry.OldValues[0].AsInt() != 1 {
		t.Errorf("expected oldValues[0] = 1, got %v", entry.OldValues[0])
	}
	if entry.OldValues[1].AsText() != "a" {
		t.Errorf("expected oldValues[1] = 'a' (from UPDATE old), got %v", entry.OldValues[1])
	}
	if len(entry.NewValues) != 0 {
		t.Errorf("expected no new values for DELETE, got %d", len(entry.NewValues))
	}
}

func TestConcatThreeFiles(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_three.changeset")

	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}

	// File 1: INSERT pk=1, "a"
	path1 := filepath.Join(t.TempDir(), "concat_3a.changeset")
	w1, _ := NewWriter(path1)
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("a")}})
	w1.Close()

	// File 2: UPDATE pk=1, "a"→"b"
	path2 := filepath.Join(t.TempDir(), "concat_3b.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1), NewValueText("a")},
		NewValues: []Value{NewValueUndefined(), NewValueText("b")},
	})
	w2.Close()

	// File 3: UPDATE pk=1, "b"→"c"
	path3 := filepath.Join(t.TempDir(), "concat_3c.changeset")
	w3, _ := NewWriter(path3)
	w3.BeginTable(table)
	w3.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1), NewValueText("b")},
		NewValues: []Value{NewValueUndefined(), NewValueText("c")},
	})
	w3.Close()

	if err := ConcatChangesets([]string{path1, path2, path3}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry == nil {
		t.Fatal("expected an entry")
	}
	// INSERT + UPDATE + UPDATE → modified INSERT with "c"
	if entry.Op != OpInsert {
		t.Errorf("expected OpInsert, got %v", entry.Op)
	}
	if entry.NewValues[1].AsText() != "c" {
		t.Errorf("expected 'c', got %v", entry.NewValues[1])
	}
}

func TestConcatSameTableDifferentPKs(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_multipk.changeset")

	table := ChangesetTable{Name: "t", PrimaryKeys: []bool{true, false}}

	// File 1: INSERT pk=1, INSERT pk=3
	path1 := filepath.Join(t.TempDir(), "concat_mp1.changeset")
	w1, _ := NewWriter(path1)
	w1.BeginTable(table)
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(1), NewValueText("a")}})
	w1.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(3), NewValueText("c")}})
	w1.Close()

	// File 2: INSERT pk=2, DELETE pk=3, INSERT pk=4
	path2 := filepath.Join(t.TempDir(), "concat_mp2.changeset")
	w2, _ := NewWriter(path2)
	w2.BeginTable(table)
	w2.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(2), NewValueText("b")}})
	w2.WriteEntry(ChangesetEntry{Op: OpDelete, OldValues: []Value{NewValueInt(3), NewValueText("c")}})
	w2.WriteEntry(ChangesetEntry{Op: OpInsert, NewValues: []Value{NewValueInt(4), NewValueText("d")}})
	w2.Close()

	if err := ConcatChangesets([]string{path1, path2}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	// Should have: INSERT pk=1, INSERT pk=2, INSERT pk=4 (pk=3 canceled by insert+delete)
	seen := map[int64]string{}
	for {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil {
			break
		}
		if entry.Op != OpInsert {
			t.Errorf("expected OpInsert, got %v", entry.Op)
		}
		seen[entry.NewValues[0].AsInt()] = entry.NewValues[1].AsText()
	}

	if len(seen) != 3 {
		t.Errorf("expected 3 entries, got %d: %v", len(seen), seen)
	}
	if seen[1] != "a" || seen[2] != "b" || seen[4] != "d" {
		t.Errorf("unexpected entries: %v", seen)
	}
	// pk=3 should not be present
	if _, ok := seen[3]; ok {
		t.Error("pk=3 should have been cancelled")
	}
}

func TestConcatEmptyInput(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "concat_empty.changeset")

	if err := ConcatChangesets([]string{}, outPath); err != nil {
		t.Fatal(err)
	}

	r, _ := NewReader(outPath)
	defer r.Close()

	entry, _ := r.NextEntry()
	if entry != nil {
		t.Error("expected no entries for empty input")
	}
}
