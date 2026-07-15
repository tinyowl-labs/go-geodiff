package changeset

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkReaderInsert measures the throughput of reading a changeset
// with many simple INSERT entries.
func BenchmarkReaderInsert(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench_insert.changeset")
	w, _ := NewWriter(path)
	table := ChangesetTable{
		Name:        "points",
		PrimaryKeys: []bool{true, false},
	}
	w.BeginTable(table)
	const numEntries = 10000
	for i := 0; i < numEntries; i++ {
		w.WriteEntry(ChangesetEntry{
			Op: OpInsert,
			NewValues: []Value{
				NewValueInt(int64(i)),
				NewValueText("benchmark_value"),
			},
		})
	}
	w.Close()

	data, _ := os.ReadFile(path)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		r := &Reader{buf: data}
		for {
			entry, err := r.NextEntry()
			if err != nil {
				b.Fatal(err)
			}
			if entry == nil {
				break
			}
		}
	}
}

// BenchmarkWriterInsert measures the throughput of writing INSERT entries.
func BenchmarkWriterInsert(b *testing.B) {
	table := ChangesetTable{
		Name:        "points",
		PrimaryKeys: []bool{true, false},
	}
	entry := ChangesetEntry{
		Op: OpInsert,
		NewValues: []Value{
			NewValueInt(42),
			NewValueText("hello world"),
		},
	}

	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		path := filepath.Join(b.TempDir(), "bench_write.changeset")
		w, err := NewWriter(path)
		if err != nil {
			b.Fatal(err)
		}
		w.BeginTable(table)
		for j := 0; j < 1000; j++ {
			w.WriteEntry(entry)
		}
		w.Close()
	}
}

// BenchmarkConcatChangesets measures the throughput of concatenating changesets.
func BenchmarkConcatChangesets(b *testing.B) {
	// Create two input changesets
	tmpDir := b.TempDir()
	fileA := filepath.Join(tmpDir, "a.changeset")
	fileB := filepath.Join(tmpDir, "b.changeset")

	table := ChangesetTable{
		Name:        "t",
		PrimaryKeys: []bool{true, false},
	}

	// File A: 500 INSERTs
	wA, _ := NewWriter(fileA)
	wA.BeginTable(table)
	for i := 0; i < 500; i++ {
		wA.WriteEntry(ChangesetEntry{
			Op: OpInsert,
			NewValues: []Value{
				NewValueInt(int64(i)),
				NewValueText("data"),
			},
		})
	}
	wA.Close()

	// File B: 500 UPDATEs
	wB, _ := NewWriter(fileB)
	wB.BeginTable(table)
	for i := 0; i < 500; i++ {
		wB.WriteEntry(ChangesetEntry{
			Op: OpUpdate,
			OldValues: []Value{
				NewValueInt(int64(i)),
				NewValueText("data"),
			},
			NewValues: []Value{
				NewValueUndefined(),
				NewValueText("updated"),
			},
		})
	}
	wB.Close()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		out := filepath.Join(tmpDir, "concat_out.changeset")
		if err := ConcatChangesets([]string{fileA, fileB}, out); err != nil {
			b.Fatal(err)
		}
		os.Remove(out)
	}
}
