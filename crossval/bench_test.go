// Package crossval provides performance benchmarks for go-geodiff.
//
// Run Go benchmarks only:
//
//	go test ./crossval/ -bench=. -benchtime=3s
//
// To compare against C++ geodiff (requires GEODIFF_CPP_BIN):
//
//	GEODIFF_CPP_BIN=/path/to/geodiff go test ./crossval/ -bench='GoVsCPP' -benchtime=3s
//
// To print a quick performance summary:
//
//	go test ./crossval/ -run TestPrintPerformanceSummary -v
package crossval

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
)

// findProjectRoot walks up to find the module root (where go.mod lives).
func findProjectRoot(tb testing.TB) string {
	tb.Helper()
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func testdataPath(tb testing.TB, rel string) string {
	tb.Helper()
	p := filepath.Join(findProjectRoot(tb), "testdata", rel)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		tb.Skipf("testdata not found: %s", p)
	}
	return p
}

// ---------------------------------------------------------------------------
// Go benchmarks — real GPKG files from testdata
// ---------------------------------------------------------------------------

func BenchmarkCreateChangeset(b *testing.B) {
	base := testdataPath(b, "1_geopackage/modified_1_geom.gpkg")
	out := filepath.Join(b.TempDir(), "diff.bin")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := geodiff.CreateChangeset(base, base, out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSchema(b *testing.B) {
	base := testdataPath(b, "1_geopackage/modified_1_geom.gpkg")
	out := filepath.Join(b.TempDir(), "schema.json")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := geodiff.Schema("sqlite", "", base, out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDumpData(b *testing.B) {
	base := testdataPath(b, "1_geopackage/modified_1_geom.gpkg")
	out := filepath.Join(b.TempDir(), "dump.bin")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := geodiff.DumpData("sqlite", "", base, out); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMakeCopy(b *testing.B) {
	base := testdataPath(b, "1_geopackage/modified_1_geom.gpkg")
	dst := filepath.Join(b.TempDir(), "copy.gpkg")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := geodiff.MakeCopySqlite(base, dst); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// C++ comparison benchmarks (only run when GEODIFF_CPP_BIN is set)
// ---------------------------------------------------------------------------

func BenchmarkCreateChangeset_GoVsCPP(b *testing.B) {
	bin := cppBin()
	if bin == "" {
		b.Skip("C++ geodiff not available; set GEODIFF_CPP_BIN")
	}
	base := testdataPath(b, "1_geopackage/modified_1_geom.gpkg")

	b.Run("Go", func(b *testing.B) {
		out := filepath.Join(b.TempDir(), "go_diff.bin")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := geodiff.CreateChangeset(base, base, out); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("CPP", func(b *testing.B) {
		out := filepath.Join(b.TempDir(), "cpp_diff.bin")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			cmd := exec.Command(bin, "diff", base, base, out)
			if err := cmd.Run(); err != nil {
				b.Fatalf("C++ geodiff failed: %v", err)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// Quick performance summary (test, not benchmark)
// ---------------------------------------------------------------------------

func TestPrintPerformanceSummary(t *testing.T) {
	base := testdataPath(t, "1_geopackage/modified_1_geom.gpkg")

	type result struct {
		name string
		nsOp int64
	}
	var results []result

	br := testing.Benchmark(func(b *testing.B) {
		out := filepath.Join(b.TempDir(), "diff.bin")
		for i := 0; i < b.N; i++ {
			geodiff.CreateChangeset(base, base, out)
		}
	})
	results = append(results, result{"CreateChangeset (identical, 1 table, 4 cols)", br.NsPerOp()})

	br = testing.Benchmark(func(b *testing.B) {
		out := filepath.Join(b.TempDir(), "schema.json")
		for i := 0; i < b.N; i++ {
			geodiff.Schema("sqlite", "", base, out)
		}
	})
	results = append(results, result{"Schema export", br.NsPerOp()})

	br = testing.Benchmark(func(b *testing.B) {
		out := filepath.Join(b.TempDir(), "dump.bin")
		for i := 0; i < b.N; i++ {
			geodiff.DumpData("sqlite", "", base, out)
		}
	})
	results = append(results, result{"DumpData", br.NsPerOp()})

	t.Logf("\n %-50s %12s", "Operation", "ns/op")
	t.Logf(" %s", "-----------------------------------------------------------------")
	for _, r := range results {
		t.Logf(" %-50s %12d", r.name, r.nsOp)
	}

	bin := cppBin()
	if bin != "" {
		t.Logf("\n C++ geodiff found at: %s", bin)
		t.Logf(" Run: GEODIFF_CPP_BIN=%s go test ./crossval/ -bench=GoVsCPP -benchtime=3s", bin)
	} else {
		t.Logf("\n C++ geodiff not found. To compare Go vs C++:")
		t.Logf("   1. Build C++ geodiff: https://github.com/MerginMaps/geodiff")
		t.Logf("   2. Run: GEODIFF_CPP_BIN=/path/to/geodiff go test ./crossval/ -bench=GoVsCPP -benchtime=3s")
	}
}
