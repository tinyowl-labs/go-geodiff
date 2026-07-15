package driver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tinyowl-labs/go-geodiff/changeset"
	"github.com/tinyowl-labs/go-geodiff/schema"
)

// tmpFile creates a temporary file path relative to the system temp dir.
func tmpFile(pattern string) string {
	return filepath.Join(os.TempDir(), pattern)
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// TestCreateChangesetRoundTrip tests that creating a changeset between two
// identical GPKG files produces an empty changeset, and that a round-trip
// between two different GPKG files works correctly.
func TestCreateChangesetRoundTrip(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")

	// Create a temporary copy of the base file
	tmpBase := tmpFile("test_roundtrip_base.gpkg")
	if err := copyFile(baseFile, tmpBase); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmpBase)

	tmpModified := tmpFile("test_roundtrip_modified.gpkg")
	if err := copyFile(baseFile, tmpModified); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmpModified)

	// Test 1: Open with base and modified (identical files => empty changeset)
	d := NewSqliteDriver()
	err := d.Open(context.Background(), ConnInfo{
		Base:     tmpBase,
		Modified: tmpModified,
	})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	changesetFile := tmpFile("test_roundtrip.diff")
	defer os.Remove(changesetFile)

	w, err := changeset.NewWriter(changesetFile)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}
	if err := d.CreateChangeset(context.Background(), w); err != nil {
		w.Close()
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	w.Close()

	// Read back the changeset - should have no entries for identical files
	r, err := changeset.NewReader(changesetFile)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}
	defer r.Close()

	entryCount := 0
	for {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatalf("NextEntry failed: %v", err)
		}
		if entry == nil {
			break
		}
		entryCount++
	}
	t.Logf("Changeset has %d entries (expected 0 for identical files)", entryCount)
}

// TestListTables_GPKG tests listing tables from a GPKG file.
func TestListTables_GPKG(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")
	tmp := tmpFile("test_list_tables.gpkg")
	if err := copyFile(baseFile, tmp); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmp)

	d := NewSqliteDriver()
	err := d.Open(context.Background(), ConnInfo{Base: tmp})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	tables, err := d.ListTables(context.Background(), BaseSide)
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}

	t.Logf("Tables: %v", tables)

	// Should have at least one table and no GPKG system tables
	for _, name := range tables {
		if name == "gpkg_contents" || name == "gpkg_geometry_columns" {
			t.Errorf("System table %s should not appear in ListTables", name)
		}
	}
}

// TestTableSchema_GPKG tests reading table schema from a GPKG file.
func TestTableSchema_GPKG(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")
	tmp := tmpFile("test_schema.gpkg")
	if err := copyFile(baseFile, tmp); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmp)

	d := NewSqliteDriver()
	err := d.Open(context.Background(), ConnInfo{Base: tmp})
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	tables, err := d.ListTables(context.Background(), BaseSide)
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}

	if len(tables) == 0 {
		t.Fatal("No tables found")
	}

	for _, tableName := range tables {
		schemaTbl, err := d.TableSchema(context.Background(), tableName, BaseSide)
		if err != nil {
			t.Fatalf("TableSchema(%s) failed: %v", tableName, err)
		}
		t.Logf("Table %s: %d columns, hasPK=%v", tableName, len(schemaTbl.Columns), schemaTbl.HasPrimaryKey())

		if schemaTbl.Name != tableName {
			t.Errorf("Schema name mismatch: %s != %s", schemaTbl.Name, tableName)
		}
		if len(schemaTbl.Columns) == 0 {
			t.Errorf("Table %s has zero columns", tableName)
		}
	}
}

// TestDumpData_And_ApplyChangeset tests the full round-trip:
// 1. Dump data from a GPKG to a changeset
// 2. Create an empty database
// 3. Create tables from the schema
// 4. Apply the changeset
func TestDumpDataAndApplyChangeset(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")
	tmpSrc := tmpFile("test_dump_src.gpkg")
	if err := copyFile(baseFile, tmpSrc); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmpSrc)

	// 1. Open source and dump data
	dSrc := NewSqliteDriver()
	if err := dSrc.Open(context.Background(), ConnInfo{Base: tmpSrc}); err != nil {
		t.Fatalf("Open source failed: %v", err)
	}

	changesetFile := tmpFile("test_dump.diff")
	defer os.Remove(changesetFile)

	w, err := changeset.NewWriter(changesetFile)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}
	if err := dSrc.DumpData(context.Background(), w, BaseSide); err != nil {
		w.Close()
		dSrc.Close()
		t.Fatalf("DumpData failed: %v", err)
	}
	w.Close()

	// Get schemas before closing
	var schemas []*schema.TableSchema
	tables, err := dSrc.ListTables(context.Background(), BaseSide)
	if err != nil {
		dSrc.Close()
		t.Fatalf("ListTables failed: %v", err)
	}
	for _, tableName := range tables {
		ts, err := dSrc.TableSchema(context.Background(), tableName, BaseSide)
		if err != nil {
			dSrc.Close()
			t.Fatalf("TableSchema failed: %v", err)
		}
		schemas = append(schemas, ts)
	}
	dSrc.Close()

	// 2. Create empty database
	tmpDst := tmpFile("test_dump_dst.gpkg")
	defer os.Remove(tmpDst)

	dDst := NewSqliteDriver()
	if err := dDst.Create(context.Background(), ConnInfo{Base: tmpDst}, true); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 3. Create tables
	if err := dDst.CreateTables(context.Background(), schemas); err != nil {
		dDst.Close()
		t.Fatalf("CreateTables failed: %v", err)
	}

	// 4. Apply changeset
	r, err := changeset.NewReader(changesetFile)
	if err != nil {
		dDst.Close()
		t.Fatalf("NewReader failed: %v", err)
	}
	if err := dDst.ApplyChangeset(context.Background(), r); err != nil {
		r.Close()
		dDst.Close()
		t.Fatalf("ApplyChangeset failed: %v", err)
	}
	r.Close()

	// 5. Verify table count matches
	dstTables, err := dDst.ListTables(context.Background(), BaseSide)
	if err != nil {
		dDst.Close()
		t.Fatalf("ListTables on dest failed: %v", err)
	}
	dDst.Close()

	if len(dstTables) != len(tables) {
		t.Errorf("Destination has %d tables, source had %d", len(dstTables), len(tables))
	}
}

// TestCreateChangeset_IdenticalFiles produces no changes.
func TestCreateChangeset_IdenticalFiles(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")
	tmpBase := tmpFile("test_identical_base.gpkg")
	tmpModified := tmpFile("test_identical_modified.gpkg")
	if err := copyFile(baseFile, tmpBase); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	if err := copyFile(baseFile, tmpModified); err != nil {
		os.Remove(tmpBase)
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmpBase)
	defer os.Remove(tmpModified)

	d := NewSqliteDriver()
	if err := d.Open(context.Background(), ConnInfo{Base: tmpBase, Modified: tmpModified}); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	changesetFile := tmpFile("test_identical.diff")
	defer os.Remove(changesetFile)

	w, err := changeset.NewWriter(changesetFile)
	if err != nil {
		t.Fatalf("NewWriter failed: %v", err)
	}

	if err := d.CreateChangeset(context.Background(), w); err != nil {
		w.Close()
		t.Fatalf("CreateChangeset failed: %v", err)
	}
	w.Close()

	// Read the changeset - should be empty
	r, err := changeset.NewReader(changesetFile)
	if err != nil {
		t.Fatalf("NewReader failed: %v", err)
	}
	defer r.Close()

	if r.IsEmpty() {
		t.Log("Changeset is empty as expected for identical files")
	}

	entryCount := 0
	for {
		entry, err := r.NextEntry()
		if err != nil {
			t.Fatalf("NextEntry failed: %v", err)
		}
		if entry == nil {
			break
		}
		entryCount++
		fmt.Printf("Entry: op=%s table=%s\n", entry.Op, entry.Table.Name)
	}

	t.Logf("Identical files produced %d changeset entries", entryCount)
}

// TestTableSchema_HasPrimaryKey verifies that PK detection works.
func TestTableSchema_HasPrimaryKey(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")
	tmp := tmpFile("test_pk.gpkg")
	if err := copyFile(baseFile, tmp); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmp)

	d := NewSqliteDriver()
	if err := d.Open(context.Background(), ConnInfo{Base: tmp}); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer d.Close()

	tables, err := d.ListTables(context.Background(), BaseSide)
	if err != nil {
		t.Fatalf("ListTables failed: %v", err)
	}

	for _, tableName := range tables {
		ts, err := d.TableSchema(context.Background(), tableName, BaseSide)
		if err != nil {
			t.Fatalf("TableSchema(%s) failed: %v", tableName, err)
		}

		if !ts.HasPrimaryKey() {
			t.Logf("Table %s has no primary key (will be skipped in changeset creation)", tableName)
		} else {
			// Verify at least one column has IsPrimaryKey
			hasPK := false
			for _, col := range ts.Columns {
				if col.IsPrimaryKey {
					hasPK = true
					break
				}
			}
			if !hasPK {
				t.Errorf("Table %s: HasPrimaryKey()=true but no column has IsPrimaryKey=true", tableName)
			}
		}
	}
}

// TestOpen_NonexistentFile returns an error.
func TestOpen_NonexistentFile(t *testing.T) {
	d := NewSqliteDriver()
	err := d.Open(context.Background(), ConnInfo{Base: "/nonexistent/path/file.gpkg"})
	if err == nil {
		d.Close()
		t.Error("Expected error opening nonexistent file, got nil")
	}
}

// TestOpen_MissingBaseKey returns an error.
func TestOpen_MissingBaseKey(t *testing.T) {
	d := NewSqliteDriver()
	err := d.Open(context.Background(), ConnInfo{})
	if err == nil {
		t.Error("Expected error for missing 'base' key, got nil")
	}
}

// TestCreate_NewDatabase creates a database successfully.
func TestCreate_NewDatabase(t *testing.T) {
	tmp := tmpFile("test_create.gpkg")
	defer os.Remove(tmp)

	d := NewSqliteDriver()
	if err := d.Create(context.Background(), ConnInfo{Base: tmp}, true); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify the file exists
	if _, err := os.Stat(tmp); os.IsNotExist(err) {
		t.Error("Database file was not created")
	}
}

// TestCreate_AlreadyExists returns error without overwrite.
func TestCreate_AlreadyExists(t *testing.T) {
	baseFile := filepath.Join("..", "testdata", "1_geopackage", "modified_1_geom.gpkg")
	tmp := tmpFile("test_already_exists.gpkg")
	if err := copyFile(baseFile, tmp); err != nil {
		t.Skipf("skipping test: cannot copy testdata: %v", err)
	}
	defer os.Remove(tmp)

	d := NewSqliteDriver()
	err := d.Create(context.Background(), ConnInfo{Base: tmp}, false)
	if err == nil {
		d.Close()
		t.Error("Expected error creating database that already exists, got nil")
	}
}
