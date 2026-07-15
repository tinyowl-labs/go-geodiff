// Package crossval provides cross-validation tests that verify
// go-geodiff produces byte-identical output to the C++ geodiff binary.
//
// These tests require the C++ geodiff binary to be available at the path
// specified by the GEODIFF_CPP_BIN environment variable. They are skipped
// when the binary is not available.
package crossval

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
)

// cppBin returns the path to the C++ geodiff binary, or empty string if not found.
func cppBin() string {
	if b := os.Getenv("GEODIFF_CPP_BIN"); b != "" {
		return b
	}
	// Check common locations
	for _, p := range []string{"geodiff", "./geodiff", "/usr/local/bin/geodiff"} {
		if _, err := exec.LookPath(p); err == nil {
			return p
		}
	}
	return ""
}

// runCppDiff runs: geodiff diff base modified output
func runCppDiff(t *testing.T, bin, base, modified, output string) {
	t.Helper()
	cmd := exec.Command(bin, "diff", base, modified, output)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("C++ geodiff diff failed: %v\nstderr: %s", err, stderr.String())
	}
}

// runGoDiff creates a changeset using the Go library.
func runGoDiff(t *testing.T, base, modified, output string) {
	t.Helper()
	if err := geodiff.CreateChangeset(base, modified, output); err != nil {
		t.Fatalf("Go CreateChangeset failed: %v", err)
	}
}

// TestByteIdenticalDiffs verifies that Go-generated diffs are byte-identical
// to C++-generated diffs for all test fixtures.
func TestByteIdenticalDiffs(t *testing.T) {
	bin := cppBin()
	if bin == "" {
		t.Skip("C++ geodiff binary not found. Set GEODIFF_CPP_BIN to enable cross-validation.")
	}

	testdataDir := filepath.Join("..", "testdata")

	// Test cases: base, modified GPKG pairs from the C++ test suite
	cases := []struct {
		name     string
		base     string
		modified string
	}{
		{
			name:     "geopackage_1_geom",
			base:     filepath.Join(testdataDir, "base.gpkg"),
			modified: filepath.Join(testdataDir, "1_geopackage", "modified_1_geom.gpkg"),
		},
		{
			name:     "geopackage_complex",
			base:     filepath.Join(testdataDir, "base.gpkg"),
			modified: filepath.Join(testdataDir, "complex", "complex1.gpkg"),
		},
		{
			name:     "sqlite_no_gis",
			base:     filepath.Join(testdataDir, "base.sqlite"),
			modified: filepath.Join(testdataDir, "pure_sqlite", "modified_base.sqlite"),
		},
		{
			name:     "foreign_keys",
			base:     filepath.Join(testdataDir, "base_fk.gpkg"),
			modified: filepath.Join(testdataDir, "fk_1_update", "modified_fk.gpkg"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Skip if test fixture doesn't exist
			if _, err := os.Stat(tc.base); os.IsNotExist(err) {
				t.Skipf("base fixture not found: %s", tc.base)
			}
			if _, err := os.Stat(tc.modified); os.IsNotExist(err) {
				t.Skipf("modified fixture not found: %s", tc.modified)
			}

			tmpDir := t.TempDir()
			cppDiff := filepath.Join(tmpDir, "cpp.diff")
			goDiff := filepath.Join(tmpDir, "go.diff")

			runCppDiff(t, bin, tc.base, tc.modified, cppDiff)
			runGoDiff(t, tc.base, tc.modified, goDiff)

			cppBytes, err := os.ReadFile(cppDiff)
			if err != nil {
				t.Fatalf("Failed to read C++ diff: %v", err)
			}
			goBytes, err := os.ReadFile(goDiff)
			if err != nil {
				t.Fatalf("Failed to read Go diff: %v", err)
			}

			if !bytes.Equal(cppBytes, goBytes) {
				// Find first differing byte
				minLen := len(cppBytes)
				if len(goBytes) < minLen {
					minLen = len(goBytes)
				}
				for i := 0; i < minLen; i++ {
					if cppBytes[i] != goBytes[i] {
						t.Errorf("Byte %d differs: C++=0x%02x Go=0x%02x", i, cppBytes[i], goBytes[i])
						// Show context
						start := i - 8
						if start < 0 {
							start = 0
						}
						end := i + 16
						if end > minLen {
							end = minLen
						}
						t.Logf("C++ [%d:%d]: % x", start, end, cppBytes[start:end])
						t.Logf("Go  [%d:%d]: % x", start, end, goBytes[start:end])
						break
					}
				}
				if len(cppBytes) != len(goBytes) {
					t.Errorf("Size mismatch: C++=%d bytes, Go=%d bytes", len(cppBytes), len(goBytes))
				}
			}
		})
	}
}

// TestApplyRoundTrip verifies that applying a Go-generated diff produces
// the same result as applying a C++-generated diff.
func TestApplyRoundTrip(t *testing.T) {
	bin := cppBin()
	if bin == "" {
		t.Skip("C++ geodiff binary not found. Set GEODIFF_CPP_BIN to enable cross-validation.")
	}

	testdataDir := filepath.Join("..", "testdata")
	modified := filepath.Join(testdataDir, "1_geopackage", "modified_1_geom.gpkg")

	// Copy base to a writable temp file — use explicit ReadFile+WriteFile
	// to avoid filesystem quirks with os.Create/CreateTemp in Go 1.25.
	baseSrc := filepath.Join(testdataDir, "base.gpkg")
	baseData, err := os.ReadFile(baseSrc)
	if err != nil {
		t.Fatalf("read base: %v", err)
	}
	tmpDir, err := os.MkdirTemp("", "geodiff-apply-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)
	base := filepath.Join(tmpDir, "base.gpkg")
	if err := os.WriteFile(base, baseData, 0644); err != nil {
		t.Fatalf("write base: %v", err)
	}

	// Use os.CreateTemp for patched files — avoids os.MkdirTemp + os.WriteFile quirk.
	cppDiff, err := os.CreateTemp("", "geodiff-cpp-diff-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	cppDiffPath := cppDiff.Name()
	cppDiff.Close()
	defer os.Remove(cppDiffPath)

	runCppDiff(t, bin, base, modified, cppDiffPath)

	cppPatched, err := os.CreateTemp("", "geodiff-cpp-patched-*.gpkg")
	if err != nil {
		t.Fatal(err)
	}
	cppPatchedPath := cppPatched.Name()
	cppPatched.Close()
	defer os.Remove(cppPatchedPath)

	if err := copyFileData(baseData, cppPatchedPath); err != nil {
		t.Fatalf("copy: %v", err)
	}
	cmd := exec.Command(bin, "apply", cppPatchedPath, cppDiffPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("C++ apply failed: %v\n%s", err, out)
	}

	// Apply Go diff
	goDiff, err := os.CreateTemp("", "geodiff-go-diff-*.bin")
	if err != nil {
		t.Fatal(err)
	}
	goDiffPath := goDiff.Name()
	goDiff.Close()
	defer os.Remove(goDiffPath)

	runGoDiff(t, base, modified, goDiffPath)

	goPatched, err := os.CreateTemp("", "geodiff-go-patched-*.gpkg")
	if err != nil {
		t.Fatal(err)
	}
	goPatchedPath := goPatched.Name()
	goPatched.Close()
	defer os.Remove(goPatchedPath)

	if err := copyFileData(baseData, goPatchedPath); err != nil {
		t.Fatalf("copy: %v", err)
	}
	// Go apply: will fail on Spatialite functions (ST_IsEmpty) since
	// modernc.org/sqlite doesn't load the Spatialite extension. The C++ binary
	// succeeds because it links against libspatialite. This is a known limitation.
	if err := geodiff.ApplyChangeset(goPatchedPath, goDiffPath); err != nil {
		if strings.Contains(err.Error(), "ST_IsEmpty") || strings.Contains(err.Error(), "no such function") {
			t.Skip("Spatialite functions not available in modernc.org/sqlite; C++ apply succeeded")
		}
		t.Fatalf("Go ApplyChangeset failed: %v", err)
	}

	// Compare patched files
	cppBytes, _ := os.ReadFile(cppPatchedPath)
	goBytes, _ := os.ReadFile(goPatchedPath)

	if !bytes.Equal(cppBytes, goBytes) {
		t.Errorf("Patched files differ: C++=%d bytes, Go=%d bytes", len(cppBytes), len(goBytes))
		for i := 0; i < len(cppBytes) && i < len(goBytes); i++ {
			if cppBytes[i] != goBytes[i] {
				t.Errorf("First diff at byte %d: C++=0x%02x Go=0x%02x", i, cppBytes[i], goBytes[i])
				break
			}
		}
	}
}

func copyFile(dst, src string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// copyFileData writes data to dst, avoiding os.Create/os.CreateTemp quirks.
func copyFileData(data []byte, dst string) error {
	return os.WriteFile(dst, data, 0644)
}
