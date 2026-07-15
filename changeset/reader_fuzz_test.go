package changeset

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzReaderRoundTrip verifies that the reader handles arbitrary binary input
// without panicking. Malformed input should produce clean errors, never panics.
func FuzzReader(f *testing.F) {
	// Seed corpus with valid changesets.
	seeds := []struct {
		name string
		data []byte
	}{
		{
			name: "valid_insert",
			data: validInsertChangeset(),
		},
		{
			name: "valid_update",
			data: validUpdateChangeset(),
		},
		{
			name: "valid_delete",
			data: validDeleteChangeset(),
		},
		{
			name: "empty",
			data: []byte{},
		},
		{
			name: "truncated_table_header",
			data: []byte{'T', 0x01, 0x01},
		},
		{
			name: "invalid_opcode",
			data: append(validTableHeader(), 0xFF),
		},
		{
			name: "truncated_int",
			data: append(validInsertPrefix(), byte(TypeInt)),
		},
		{
			name: "truncated_double",
			data: append(validInsertPrefix(), byte(TypeDouble), 0x00, 0x00),
		},
		{
			name: "truncated_text_length",
			data: append(validInsertPrefix(), byte(TypeText), 0x80),
		},
		{
			name: "truncated_blob_length",
			data: append(validInsertPrefix(), byte(TypeBlob), 0xFF, 0x7F),
		},
	}

	for _, seed := range seeds {
		f.Add(seed.data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Write fuzz input to a temp file.
		path := filepath.Join(t.TempDir(), "fuzz.changeset")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Skip()
		}

		r, err := NewReader(path)
		if err != nil {
			t.Skip() // file creation issues are not our concern
		}
		defer r.Close()

		// Read all entries — must not panic on any input.
		for {
			entry, err := r.NextEntry()
			if err != nil {
				// Error is expected for malformed input.
				break
			}
			if entry == nil {
				break
			}
		}
	})
}

// Helper: create a valid table header (1 col, PK, name "t").
func validTableHeader() []byte {
	w, _ := NewWriter(filepath.Join(os.TempDir(), "fuzz_seed_header.changeset"))
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.Close()

	data, _ := os.ReadFile(filepath.Join(os.TempDir(), "fuzz_seed_header.changeset"))
	os.Remove(filepath.Join(os.TempDir(), "fuzz_seed_header.changeset"))
	return data
}

// Helper: create a valid insert changeset.
func validInsertChangeset() []byte {
	path := filepath.Join(os.TempDir(), "fuzz_seed_insert.changeset")
	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpInsert,
		NewValues: []Value{NewValueInt(42)},
	})
	w.Close()

	data, _ := os.ReadFile(path)
	os.Remove(path)
	return data
}

// Helper: create a valid update changeset.
func validUpdateChangeset() []byte {
	path := filepath.Join(os.TempDir(), "fuzz_seed_update.changeset")
	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true},
	}
	w.BeginTable(table)
	w.WriteEntry(ChangesetEntry{
		Op:        OpUpdate,
		OldValues: []Value{NewValueInt(1)},
		NewValues: []Value{NewValueInt(2)},
	})
	w.Close()

	data, _ := os.ReadFile(path)
	os.Remove(path)
	return data
}

// Helper: create a valid delete changeset.
func validDeleteChangeset() []byte {
	path := filepath.Join(os.TempDir(), "fuzz_seed_delete.changeset")
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

	data, _ := os.ReadFile(path)
	os.Remove(path)
	return data
}

// Helper: create a prefix of a valid insert (table header + opcode + indirect flag).
func validInsertPrefix() []byte {
	// Table header: 'T', varint(1 col), pk byte, null-terminated name
	return []byte{
		'T',
		0x01,      // varint: 1 column
		0x01,      // PK flag
		't', 0x00, // table name + null
		byte(OpInsert), // opcode
		0x00,           // indirect flag (always 0)
	}
}
