package crossval

import (
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/geodiff"
	_ "modernc.org/sqlite"
)

// The fixtures in testdata/gpkg_rtree are GDAL GeoPackages with the default
// spatial index. Applying a changeset runs the rtree_* triggers, which call
// ST_IsEmpty and ST_MinX/MaxX/MinY/MaxY (tinyowl-labs/go-geodiff#3).

// indexedState dumps what an apply must get right: the features, the R-tree
// rows GDAL's triggers maintain, the triggers themselves and the feature
// counts.
func indexedState(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queries := []string{
		"SELECT fid, name, hex(geom) FROM shapes ORDER BY fid",
		"SELECT fid, name, hex(geom) FROM points ORDER BY fid",
		"SELECT id, minx, maxx, miny, maxy FROM rtree_shapes_geom ORDER BY id",
		"SELECT id, minx, maxx, miny, maxy FROM rtree_points_geom ORDER BY id",
		"SELECT name, sql FROM sqlite_master WHERE type = 'trigger' ORDER BY name",
		"SELECT table_name, feature_count FROM gpkg_ogr_contents ORDER BY table_name",
	}
	var b strings.Builder
	for _, q := range queries {
		fmt.Fprintf(&b, "%s\n", q)
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "  %v\n", vals)
		}
		rows.Close()
	}
	return b.String()
}

func rtreeFixtures(t *testing.T) (base, modified, dir string) {
	t.Helper()
	src := filepath.Join(findProjectRoot(t), "testdata", "gpkg_rtree")
	dir = t.TempDir()
	base = filepath.Join(dir, "base.gpkg")
	modified = filepath.Join(dir, "modified.gpkg")
	copyFileData(readFile(t, filepath.Join(src, "base.gpkg")), base)
	copyFileData(readFile(t, filepath.Join(src, "modified.gpkg")), modified)
	return base, modified, dir
}

// applyBothWays applies from→to's changeset to a copy of from with Go and,
// when the C++ binary is available, with upstream geodiff. It returns the Go
// result and the C++ one ("" without the binary) and the apply errors.
func applyBothWays(t *testing.T, dir, from, to string) (goOut, cppOut string, goErr, cppErr error) {
	t.Helper()
	diff := filepath.Join(dir, filepath.Base(from)+"-"+filepath.Base(to)+".diff")
	if err := geodiff.CreateChangeset(from, to, diff); err != nil {
		t.Fatalf("CreateChangeset: %v", err)
	}
	goOut = filepath.Join(dir, "go-"+filepath.Base(to))
	copyFileData(readFile(t, from), goOut)
	goErr = geodiff.ApplyChangeset(goOut, diff)
	if bin := cppBin(); bin != "" {
		cppOut = filepath.Join(dir, "cpp-"+filepath.Base(to))
		copyFileData(readFile(t, from), cppOut)
		if out, err := exec.Command(bin, "apply", cppOut, diff).CombinedOutput(); err != nil {
			cppErr = fmt.Errorf("%v: %s", err, out)
		}
	}
	return goOut, cppOut, goErr, cppErr
}

func TestApplyIndexedGeoPackage(t *testing.T) {
	base, modified, dir := rtreeFixtures(t)
	for _, c := range []struct{ name, from, to string }{
		{"base to modified", base, modified},
		{"modified to base", modified, base},
	} {
		t.Run(c.name, func(t *testing.T) {
			goOut, cppOut, goErr, cppErr := applyBothWays(t, dir, c.from, c.to)
			if goErr != nil {
				t.Fatalf("Go apply: %v", goErr)
			}
			want := indexedState(t, c.to)
			if got := indexedState(t, goOut); got != want {
				t.Errorf("Go apply differs from GDAL's file:\n got: %s\nwant: %s", got, want)
			}
			if cppOut == "" {
				t.Log("C++ geodiff not available; set GEODIFF_CPP_BIN to compare")
				return
			}
			if cppErr != nil {
				t.Fatalf("C++ apply: %v", cppErr)
			}
			if got, cpp := indexedState(t, goOut), indexedState(t, cppOut); got != cpp {
				t.Errorf("Go apply differs from C++:\n  go: %s\n cpp: %s", got, cpp)
			}
		})
	}
}

// A geometry the index triggers can't read fails the apply, and nothing of
// the changeset stays: no features and no R-tree rows.
func TestApplyIndexedGeoPackageRollsBack(t *testing.T) {
	base, modified, dir := rtreeFixtures(t)
	bad := filepath.Join(dir, "bad.gpkg")
	copyFileData(readFile(t, modified), bad)
	// Write the bad row without the trigger that would reject it.
	execSQL(t, bad, `DROP TRIGGER "rtree_shapes_geom_insert"`)
	execSQL(t, bad, `INSERT INTO shapes (fid, name, geom) VALUES (99, 'bad', X'00010203')`)

	before := indexedState(t, base)
	goOut, cppOut, goErr, cppErr := applyBothWays(t, dir, base, bad)
	if goErr == nil {
		t.Fatal("Go apply of an unreadable geometry succeeded")
	}
	if got := indexedState(t, goOut); got != before {
		t.Errorf("failed Go apply left changes:\n got: %s\nwant: %s", got, before)
	}
	if cppOut == "" {
		return
	}
	if cppErr == nil {
		t.Error("C++ apply of an unreadable geometry succeeded")
	}
	if got := indexedState(t, cppOut); got != before {
		t.Errorf("failed C++ apply left changes:\n got: %s\nwant: %s", got, before)
	}
}
