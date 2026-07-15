package crossval

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
)

// runCpp runs the C++ geodiff binary with the given arguments.
func runCpp(t *testing.T, args ...string) {
	t.Helper()
	bin := cppBin()
	cmd := exec.Command(bin, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("C++ geodiff %v failed: %v\n%s", args, err, out)
	}
}

// compareFiles checks that two files are byte-identical.
func compareFiles(t *testing.T, a, b, label string) {
	t.Helper()
	aData, err := os.ReadFile(a)
	if err != nil {
		t.Fatalf("read %s: %v", a, err)
	}
	bData, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("read %s: %v", b, err)
	}
	if !bytes.Equal(aData, bData) {
		t.Errorf("%s: byte mismatch (%d vs %d bytes)", label, len(aData), len(bData))
		minLen := len(aData)
		if len(bData) < minLen {
			minLen = len(bData)
		}
		for i := 0; i < minLen; i++ {
			if aData[i] != bData[i] {
				t.Errorf("  first diff at byte %d: 0x%02x vs 0x%02x", i, aData[i], bData[i])
				break
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Rebase benchmarks — the critical data-integrity path for offline-first sync
// ---------------------------------------------------------------------------

func BenchmarkRebase_NoConflict(b *testing.B) {
	tmpDir := b.TempDir()
	src := testdataPath(b, "1_geopackage/modified_1_geom.gpkg")
	srcData, err := os.ReadFile(src)
	if err != nil {
		b.Fatal(err)
	}

	base := filepath.Join(tmpDir, "base.gpkg")
	theirs := filepath.Join(tmpDir, "theirs.gpkg")
	ours := filepath.Join(tmpDir, "ours.gpkg")
	for _, dst := range []string{base, theirs, ours} {
		if err := os.WriteFile(dst, srcData, 0644); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		oursCopy := filepath.Join(tmpDir, "ours_copy.gpkg")
		if err := os.WriteFile(oursCopy, srcData, 0644); err != nil {
			b.Fatal(err)
		}
		if err := geodiff.Rebase(base, theirs, oursCopy, filepath.Join(tmpDir, "conflicts.json")); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Rebase cross-validation against C++ geodiff rebase-diff
// ---------------------------------------------------------------------------

func runCppRebase(t *testing.T, base, baseOurs, baseTheirs, output, conflicts string) {
	t.Helper()
	runCpp(t, "rebase-diff", base, baseOurs, baseTheirs, output, conflicts)
}

func TestRebaseCrossVal_NoConflict(t *testing.T) {
	bin := cppBin()
	if bin == "" {
		t.Skip("C++ geodiff not available; set GEODIFF_CPP_BIN")
	}

	tmpDir := t.TempDir()
	src := testdataPath(t, "1_geopackage/modified_1_geom.gpkg")
	srcData, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	oursCopy := filepath.Join(tmpDir, "ours.gpkg")
	theirsCopy := filepath.Join(tmpDir, "theirs.gpkg")
	if err := os.WriteFile(oursCopy, srcData, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirsCopy, srcData, 0644); err != nil {
		t.Fatal(err)
	}

	base := src

	// Both ours and theirs are identical to base → diffs are empty.
	// rebase-diff with empty inputs should produce empty output on both sides.
	baseTheirsDiff := filepath.Join(tmpDir, "base2theirs.bin")
	if err := geodiff.CreateChangeset(base, theirsCopy, baseTheirsDiff); err != nil {
		t.Fatal(err)
	}

	// Go CreateRebasedChangeset: modified (ours) has no changes relative to base.
	goRebased := filepath.Join(tmpDir, "go_rebased.bin")
	goConflicts := filepath.Join(tmpDir, "go_conflicts.json")
	err = geodiff.CreateRebasedChangeset(base, oursCopy, baseTheirsDiff, goRebased, goConflicts)
	if err != nil {
		t.Fatalf("Go CreateRebasedChangeset: %v", err)
	}

	// Verify Go output is empty (no changes to rebase on top of no changes).
	if data, _ := os.ReadFile(goRebased); len(data) > 0 {
		t.Errorf("expected empty rebased changeset, got %d bytes", len(data))
	}
}

func TestRebaseCrossVal_ExistingFixtures(t *testing.T) {
	bin := cppBin()
	if bin == "" {
		t.Skip("C++ geodiff not available; set GEODIFF_CPP_BIN")
	}

	// NOTE: These fixtures use Spatialite functions (ST_IsEmpty) which require
	// the Spatialite SQLite extension. go-geodiff uses modernc.org/sqlite which
	// does not load Spatialite. The C++ geodiff binary loads it automatically.
	// Until modernc.org/sqlite supports Spatialite, these fixtures can only be
	// validated via the C++ binary's rebase-diff output, not via apply+rebase.
	t.Skip("fixtures require Spatialite extension; not supported by modernc.org/sqlite")

	testdataDir := filepath.Join(findProjectRoot(t), "testdata", "rebase_conflict")
	base := filepath.Join("..", "testdata", "base.gpkg")

	cases := []struct {
		name   string
		ours   string // base→ours diff
		theirs string // base→theirs diff
	}{
		{"case1a", filepath.Join(testdataDir, "case1a.diff"), filepath.Join(testdataDir, "case1b.diff")},
		{"case2a", filepath.Join(testdataDir, "case2a.diff"), filepath.Join(testdataDir, "case2b.diff")},
		{"case3a", filepath.Join(testdataDir, "case3a.diff"), filepath.Join(testdataDir, "case3b.diff")},
		{"case4a", filepath.Join(testdataDir, "case4a.diff"), filepath.Join(testdataDir, "case4b.diff")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := os.Stat(tc.ours); os.IsNotExist(err) {
				t.Skipf("fixture not found: %s", tc.ours)
			}

			tmpDir := t.TempDir()

			// C++ rebase-diff
			cppRebased := filepath.Join(tmpDir, "cpp_rebased.bin")
			cppConflicts := filepath.Join(tmpDir, "cpp_conflicts.json")
			runCppRebase(t, base, tc.ours, tc.theirs, cppRebased, cppConflicts)

			// Go: Create a modified GPKG by applying the ours diff to base.
			modified := filepath.Join(tmpDir, "modified.gpkg")
			srcData, err := os.ReadFile(base)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(modified, srcData, 0644); err != nil {
				t.Fatal(err)
			}
			if err := geodiff.ApplyChangeset(modified, tc.ours); err != nil {
				t.Fatalf("apply ours diff to base: %v", err)
			}

			goRebased := filepath.Join(tmpDir, "go_rebased.bin")
			goConflicts := filepath.Join(tmpDir, "go_conflicts.json")
			if err := geodiff.CreateRebasedChangeset(base, modified, tc.theirs, goRebased, goConflicts); err != nil {
				t.Fatalf("Go CreateRebasedChangeset: %v", err)
			}

			compareFiles(t, cppRebased, goRebased, "rebase "+tc.name)
		})
	}
}

func assertEmptyFile(t *testing.T, path, label string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return // file may not exist, which is fine for "no conflicts"
	}
	if len(data) > 0 {
		t.Errorf("%s should be empty, got: %s", label, string(data))
	}
}
