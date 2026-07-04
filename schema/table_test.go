package schema

import (
	"testing"
)

// ---------------------------------------------------------------------------
// HasPrimaryKey
// ---------------------------------------------------------------------------

func TestHasPrimaryKey_NoPrimaryKey(t *testing.T) {
	s := TableSchema{
		Name: "test_table",
		Columns: []TableColumnInfo{
			{Name: "col_a", IsPrimaryKey: false},
			{Name: "col_b", IsPrimaryKey: false},
		},
	}
	if s.HasPrimaryKey() {
		t.Error("expected no primary key, but HasPrimaryKey returned true")
	}
}

func TestHasPrimaryKey_WithPrimaryKey(t *testing.T) {
	s := TableSchema{
		Name: "test_table",
		Columns: []TableColumnInfo{
			{Name: "col_a", IsPrimaryKey: false},
			{Name: "col_b", IsPrimaryKey: true},
		},
	}
	if !s.HasPrimaryKey() {
		t.Error("expected primary key, but HasPrimaryKey returned false")
	}
}

func TestHasPrimaryKey_FirstColumnIsPK(t *testing.T) {
	s := TableSchema{
		Name: "test_table",
		Columns: []TableColumnInfo{
			{Name: "fid", IsPrimaryKey: true},
			{Name: "name", IsPrimaryKey: false},
		},
	}
	if !s.HasPrimaryKey() {
		t.Error("expected primary key, but HasPrimaryKey returned false")
	}
}

// ---------------------------------------------------------------------------
// ColumnFromName
// ---------------------------------------------------------------------------

func TestColumnFromName_Found(t *testing.T) {
	s := TableSchema{
		Columns: []TableColumnInfo{
			{Name: "fid"},
			{Name: "geom"},
			{Name: "name"},
		},
	}
	idx := s.ColumnFromName("geom")
	if idx != 1 {
		t.Errorf("expected index 1 for 'geom', got %d", idx)
	}
}

func TestColumnFromName_FirstColumn(t *testing.T) {
	s := TableSchema{
		Columns: []TableColumnInfo{
			{Name: "fid"},
			{Name: "geom"},
		},
	}
	idx := s.ColumnFromName("fid")
	if idx != 0 {
		t.Errorf("expected index 0 for 'fid', got %d", idx)
	}
}

func TestColumnFromName_NotFound(t *testing.T) {
	s := TableSchema{
		Columns: []TableColumnInfo{
			{Name: "fid"},
		},
	}
	idx := s.ColumnFromName("nonexistent")
	if idx != -1 {
		t.Errorf("expected -1 for nonexistent column, got %d", idx)
	}
}

func TestColumnFromName_NoColumns(t *testing.T) {
	s := TableSchema{}
	idx := s.ColumnFromName("anything")
	if idx != -1 {
		t.Errorf("expected -1 for empty schema, got %d", idx)
	}
}

// ---------------------------------------------------------------------------
// GeometryColumn
// ---------------------------------------------------------------------------

func TestGeometryColumn_Found(t *testing.T) {
	s := TableSchema{
		Columns: []TableColumnInfo{
			{Name: "fid"},
			{Name: "geom", IsGeometry: true, GeomType: "POINT", GeomSrsId: 4326},
			{Name: "name"},
		},
	}
	idx := s.GeometryColumn()
	if idx != 1 {
		t.Errorf("expected geometry column at index 1, got %d", idx)
	}
}

func TestGeometryColumn_FirstIsGeometry(t *testing.T) {
	s := TableSchema{
		Columns: []TableColumnInfo{
			{Name: "geom", IsGeometry: true},
			{Name: "fid"},
		},
	}
	idx := s.GeometryColumn()
	if idx != 0 {
		t.Errorf("expected geometry column at index 0, got %d", idx)
	}
}

func TestGeometryColumn_NoGeometry(t *testing.T) {
	s := TableSchema{
		Columns: []TableColumnInfo{
			{Name: "fid"},
			{Name: "name"},
		},
	}
	idx := s.GeometryColumn()
	if idx != -1 {
		t.Errorf("expected -1 for no geometry column, got %d", idx)
	}
}

// ---------------------------------------------------------------------------
// Equal (exact comparison)
// ---------------------------------------------------------------------------

func TestTableSchemaEqual_Identical(t *testing.T) {
	a := TableSchema{
		Name: "my_table",
		Columns: []TableColumnInfo{
			{Name: "fid", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}, IsPrimaryKey: true},
		},
		Crs: CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326},
	}
	b := TableSchema{
		Name: "my_table",
		Columns: []TableColumnInfo{
			{Name: "fid", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}, IsPrimaryKey: true},
		},
		Crs: CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326},
	}
	if !a.Equal(b) {
		t.Error("expected schemas to be equal")
	}
}

func TestTableSchemaEqual_DifferentName(t *testing.T) {
	a := TableSchema{Name: "table_a"}
	b := TableSchema{Name: "table_b"}
	if a.Equal(b) {
		t.Error("expected schemas with different names to not be equal")
	}
}

func TestTableSchemaEqual_DifferentColumnCount(t *testing.T) {
	a := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "a"},
		},
	}
	b := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "a"},
			{Name: "b"},
		},
	}
	if a.Equal(b) {
		t.Error("expected schemas with different column counts to not be equal")
	}
}

func TestTableSchemaEqual_DifferentDBType(t *testing.T) {
	a := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "val", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}},
		},
	}
	b := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "val", Type: TableColumnType{BaseType: BaseInteger, DBType: "BIGINT"}},
		},
	}
	if a.Equal(b) {
		t.Error("expected schemas with different DB types to not be equal (exact comparison)")
	}
}

// ---------------------------------------------------------------------------
// CompareWithBaseTypes (loose comparison)
// ---------------------------------------------------------------------------

func TestTableSchemaCompareWithBaseTypes_Identical(t *testing.T) {
	a := TableSchema{
		Name: "my_table",
		Columns: []TableColumnInfo{
			{Name: "fid", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}, IsPrimaryKey: true},
		},
		Crs: CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326},
	}
	b := TableSchema{
		Name: "my_table",
		Columns: []TableColumnInfo{
			{Name: "fid", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}, IsPrimaryKey: true},
		},
		Crs: CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326},
	}
	if !a.CompareWithBaseTypes(b) {
		t.Error("expected schemas to match by base types")
	}
}

func TestTableSchemaCompareWithBaseTypes_DifferentDBType_SameBaseType(t *testing.T) {
	// Same base type, different DB-level type — should still match
	a := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "val", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}},
		},
	}
	b := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "val", Type: TableColumnType{BaseType: BaseInteger, DBType: "BIGINT"}},
		},
	}
	if !a.CompareWithBaseTypes(b) {
		t.Error("expected schemas with same base type but different DB type to match (loose comparison)")
	}
}

func TestTableSchemaCompareWithBaseTypes_DifferentBaseType(t *testing.T) {
	a := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "val", Type: TableColumnType{BaseType: BaseInteger, DBType: "INTEGER"}},
		},
	}
	b := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "val", Type: TableColumnType{BaseType: BaseText, DBType: "TEXT"}},
		},
	}
	if a.CompareWithBaseTypes(b) {
		t.Error("expected schemas with different base types to not match")
	}
}

func TestTableSchemaCompareWithBaseTypes_DifferentPK(t *testing.T) {
	a := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "id", Type: TableColumnType{BaseType: BaseInteger}, IsPrimaryKey: true},
		},
	}
	b := TableSchema{
		Name: "t",
		Columns: []TableColumnInfo{
			{Name: "id", Type: TableColumnType{BaseType: BaseInteger}, IsPrimaryKey: false},
		},
	}
	if a.CompareWithBaseTypes(b) {
		t.Error("expected schemas with different PK status to not match")
	}
}

func TestTableColumnInfoCompareWithBaseTypes_Geometry(t *testing.T) {
	a := TableColumnInfo{
		Name:       "geom",
		Type:       TableColumnType{BaseType: BaseGeometry, DBType: "POINT"},
		IsGeometry: true,
		GeomType:   "POINT",
		GeomSrsId:  4326,
		GeomHasZ:   true,
		GeomHasM:   false,
	}
	b := TableColumnInfo{
		Name:       "geom",
		Type:       TableColumnType{BaseType: BaseGeometry, DBType: "geometry(POINT, 4326)"},
		IsGeometry: true,
		GeomType:   "POINT",
		GeomSrsId:  4326,
		GeomHasZ:   true,
		GeomHasM:   false,
	}
	if !a.CompareWithBaseTypes(b) {
		t.Error("expected geometry columns with same base type to match")
	}
}

func TestTableColumnInfoCompareWithBaseTypes_DifferentGeomType(t *testing.T) {
	a := TableColumnInfo{
		Name:       "geom",
		Type:       TableColumnType{BaseType: BaseGeometry},
		IsGeometry: true,
		GeomType:   "POINT",
		GeomSrsId:  4326,
	}
	b := TableColumnInfo{
		Name:       "geom",
		Type:       TableColumnType{BaseType: BaseGeometry},
		IsGeometry: true,
		GeomType:   "LINESTRING",
		GeomSrsId:  4326,
	}
	if a.CompareWithBaseTypes(b) {
		t.Error("expected geometry columns with different geom types to not match")
	}
}

// ---------------------------------------------------------------------------
// CrsDefinition.Equal
// ---------------------------------------------------------------------------

func TestCrsEqual_Identical(t *testing.T) {
	a := CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326, Wkt: "GEOGCS[...]"}
	b := CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326, Wkt: "GEOGCS[...]"}
	if !a.Equal(b) {
		t.Error("expected identical CRS to be equal")
	}
}

func TestCrsEqual_DifferentWkt_SameAuth(t *testing.T) {
	// WKT is intentionally excluded from comparison
	a := CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326, Wkt: "GEOGCS[v1]"}
	b := CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326, Wkt: "GEOGCS[v2]"}
	if !a.Equal(b) {
		t.Error("expected CRS with different WKT but same auth to be equal (WKT excluded)")
	}
}

func TestCrsEqual_DifferentSrsId(t *testing.T) {
	a := CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326}
	b := CrsDefinition{SrsId: 3857, AuthName: "EPSG", AuthCode: 3857}
	if a.Equal(b) {
		t.Error("expected CRS with different SrsId to not be equal")
	}
}

func TestCrsEqual_DifferentAuthName(t *testing.T) {
	a := CrsDefinition{SrsId: 4326, AuthName: "EPSG", AuthCode: 4326}
	b := CrsDefinition{SrsId: 4326, AuthName: "ESRI", AuthCode: 4326}
	if a.Equal(b) {
		t.Error("expected CRS with different auth name to not be equal")
	}
}

func TestCrsEqual_DifferentAuthCode(t *testing.T) {
	a := CrsDefinition{SrsId: 1, AuthName: "EPSG", AuthCode: 4326}
	b := CrsDefinition{SrsId: 1, AuthName: "EPSG", AuthCode: 3857}
	if a.Equal(b) {
		t.Error("expected CRS with different auth code to not be equal")
	}
}

// ---------------------------------------------------------------------------
// SetGeometry
// ---------------------------------------------------------------------------

func TestSetGeometry(t *testing.T) {
	var col TableColumnInfo
	col.SetGeometry("POINT", 4326, false, true)

	if !col.IsGeometry {
		t.Error("expected IsGeometry to be true after SetGeometry")
	}
	if col.Type.BaseType != BaseGeometry {
		t.Errorf("expected BaseGeometry, got %d", col.Type.BaseType)
	}
	if col.GeomType != "POINT" {
		t.Errorf("expected GeomType 'POINT', got '%s'", col.GeomType)
	}
	if col.GeomSrsId != 4326 {
		t.Errorf("expected GeomSrsId 4326, got %d", col.GeomSrsId)
	}
	if col.GeomHasM != false {
		t.Error("expected GeomHasM to be false")
	}
	if col.GeomHasZ != true {
		t.Error("expected GeomHasZ to be true")
	}
}

// ---------------------------------------------------------------------------
// ColumnType — SQLite types
// ---------------------------------------------------------------------------

func TestColumnType_SQLite_Integer(t *testing.T) {
	ct := ColumnType(nil, "INTEGER", DriverSQLite, false)
	if ct.BaseType != BaseInteger {
		t.Errorf("expected BaseInteger, got %d", ct.BaseType)
	}
	if ct.DBType != "INTEGER" {
		t.Errorf("expected DBType 'INTEGER', got '%s'", ct.DBType)
	}
}

func TestColumnType_SQLite_Text(t *testing.T) {
	ct := ColumnType(nil, "TEXT", DriverSQLite, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Real(t *testing.T) {
	ct := ColumnType(nil, "REAL", DriverSQLite, false)
	if ct.BaseType != BaseDouble {
		t.Errorf("expected BaseDouble, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Float(t *testing.T) {
	ct := ColumnType(nil, "FLOAT", DriverSQLite, false)
	if ct.BaseType != BaseDouble {
		t.Errorf("expected BaseDouble for FLOAT, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Blob(t *testing.T) {
	ct := ColumnType(nil, "BLOB", DriverSQLite, false)
	if ct.BaseType != BaseBlob {
		t.Errorf("expected BaseBlob, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Boolean(t *testing.T) {
	ct := ColumnType(nil, "BOOLEAN", DriverSQLite, false)
	if ct.BaseType != BaseBoolean {
		t.Errorf("expected BaseBoolean, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Date(t *testing.T) {
	ct := ColumnType(nil, "DATE", DriverSQLite, false)
	if ct.BaseType != BaseDate {
		t.Errorf("expected BaseDate, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Datetime(t *testing.T) {
	ct := ColumnType(nil, "DATETIME", DriverSQLite, false)
	if ct.BaseType != BaseDatetime {
		t.Errorf("expected BaseDatetime, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_Geometry(t *testing.T) {
	ct := ColumnType(nil, "POINT", DriverSQLite, true)
	if ct.BaseType != BaseGeometry {
		t.Errorf("expected BaseGeometry, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_CaseInsensitive(t *testing.T) {
	ct := ColumnType(nil, "integer", DriverSQLite, false)
	if ct.BaseType != BaseInteger {
		t.Errorf("expected BaseInteger for lowercase 'integer', got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_SmallInt(t *testing.T) {
	ct := ColumnType(nil, "SMALLINT", DriverSQLite, false)
	if ct.BaseType != BaseInteger {
		t.Errorf("expected BaseInteger for SMALLINT, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_BigInt(t *testing.T) {
	ct := ColumnType(nil, "BIGINT", DriverSQLite, false)
	if ct.BaseType != BaseInteger {
		t.Errorf("expected BaseInteger for BIGINT, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_DoublePrecision(t *testing.T) {
	ct := ColumnType(nil, "DOUBLE PRECISION", DriverSQLite, false)
	if ct.BaseType != BaseDouble {
		t.Errorf("expected BaseDouble for DOUBLE PRECISION, got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_VarcharWithLength(t *testing.T) {
	ct := ColumnType(nil, "VARCHAR(255)", DriverSQLite, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText for VARCHAR(...), got %d", ct.BaseType)
	}
}

func TestColumnType_SQLite_UnknownTypeDefaultsToText(t *testing.T) {
	ct := ColumnType(nil, "SOME_WEIRD_TYPE", DriverSQLite, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText for unknown type, got %d", ct.BaseType)
	}
}

// ---------------------------------------------------------------------------
// ColumnType — Postgres types
// ---------------------------------------------------------------------------

func TestColumnType_Postgres_Integer(t *testing.T) {
	ct := ColumnType(nil, "integer", DriverPostgres, false)
	if ct.BaseType != BaseInteger {
		t.Errorf("expected BaseInteger, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_BigInt(t *testing.T) {
	ct := ColumnType(nil, "bigint", DriverPostgres, false)
	if ct.BaseType != BaseInteger {
		t.Errorf("expected BaseInteger for bigint, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_DoublePrecision(t *testing.T) {
	ct := ColumnType(nil, "double precision", DriverPostgres, false)
	if ct.BaseType != BaseDouble {
		t.Errorf("expected BaseDouble, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Numeric(t *testing.T) {
	ct := ColumnType(nil, "numeric(10,2)", DriverPostgres, false)
	if ct.BaseType != BaseDouble {
		t.Errorf("expected BaseDouble for numeric(...), got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Decimal(t *testing.T) {
	ct := ColumnType(nil, "decimal(5,2)", DriverPostgres, false)
	if ct.BaseType != BaseDouble {
		t.Errorf("expected BaseDouble for decimal(...), got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Boolean(t *testing.T) {
	ct := ColumnType(nil, "boolean", DriverPostgres, false)
	if ct.BaseType != BaseBoolean {
		t.Errorf("expected BaseBoolean, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Text(t *testing.T) {
	ct := ColumnType(nil, "text", DriverPostgres, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Varchar(t *testing.T) {
	ct := ColumnType(nil, "varchar", DriverPostgres, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText for varchar, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_CharacterVarying(t *testing.T) {
	ct := ColumnType(nil, "character varying(255)", DriverPostgres, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText for character varying(...), got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Uuid(t *testing.T) {
	ct := ColumnType(nil, "uuid", DriverPostgres, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText for uuid, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Bytea(t *testing.T) {
	ct := ColumnType(nil, "bytea", DriverPostgres, false)
	if ct.BaseType != BaseBlob {
		t.Errorf("expected BaseBlob for bytea, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Timestamp(t *testing.T) {
	ct := ColumnType(nil, "timestamp without time zone", DriverPostgres, false)
	if ct.BaseType != BaseDatetime {
		t.Errorf("expected BaseDatetime for timestamp, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Date(t *testing.T) {
	ct := ColumnType(nil, "date", DriverPostgres, false)
	if ct.BaseType != BaseDate {
		t.Errorf("expected BaseDate, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_Geometry(t *testing.T) {
	ct := ColumnType(nil, "geometry(POINT, 4326)", DriverPostgres, true)
	if ct.BaseType != BaseGeometry {
		t.Errorf("expected BaseGeometry, got %d", ct.BaseType)
	}
}

func TestColumnType_Postgres_UnknownTypeDefaultsToText(t *testing.T) {
	ct := ColumnType(nil, "my_custom_type", DriverPostgres, false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText for unknown type, got %d", ct.BaseType)
	}
}

// ---------------------------------------------------------------------------
// ColumnType — unknown driver
// ---------------------------------------------------------------------------

func TestColumnType_UnknownDriver(t *testing.T) {
	ct := ColumnType(nil, "INTEGER", "mysql", false)
	if ct.BaseType != BaseText {
		t.Errorf("expected BaseText fallback for unknown driver, got %d", ct.BaseType)
	}
}

// ---------------------------------------------------------------------------
// baseTypeToString
// ---------------------------------------------------------------------------

func TestBaseTypeToString(t *testing.T) {
	tests := []struct {
		bt   BaseType
		want string
	}{
		{BaseText, "text"},
		{BaseInteger, "integer"},
		{BaseDouble, "double"},
		{BaseBoolean, "boolean"},
		{BaseBlob, "blob"},
		{BaseGeometry, "geometry"},
		{BaseDate, "date"},
		{BaseDatetime, "datetime"},
		{BaseType(99), "?"},
	}
	for _, tt := range tests {
		got := baseTypeToString(tt.bt)
		if got != tt.want {
			t.Errorf("baseTypeToString(%d) = %q, want %q", tt.bt, got, tt.want)
		}
	}
}
