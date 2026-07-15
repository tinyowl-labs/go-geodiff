/*
GEODIFF - MIT License
Copyright (C) 2020 Martin Dobias

Go port of tableschema.h + tableschema.cpp
*/

package schema

import (
	"strings"
)

// Logger is a minimal logging interface used by schema functions.
// Pass nil to skip logging (e.g. in tests).
type Logger interface {
	Debug(msg string)
	Info(msg string)
	Warn(msg string)
	Error(msg string)
}

// Driver name constants used by ColumnType to dispatch to the correct parser.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// ---------------------------------------------------------------------------
// BaseType
// ---------------------------------------------------------------------------

// BaseType is a normalized SQL type identifier shared across all drivers.
type BaseType int

const (
	BaseText     BaseType = 0
	BaseInteger  BaseType = 1
	BaseDouble   BaseType = 2
	BaseBoolean  BaseType = 3
	BaseBlob     BaseType = 4
	BaseGeometry BaseType = 5
	BaseDate     BaseType = 6
	BaseDatetime BaseType = 7
)

// BaseTypeString returns a human-readable string for the base type.
func BaseTypeString(t BaseType) string {
	switch t {
	case BaseText:
		return "text"
	case BaseInteger:
		return "integer"
	case BaseDouble:
		return "double"
	case BaseBoolean:
		return "boolean"
	case BaseBlob:
		return "blob"
	case BaseGeometry:
		return "geometry"
	case BaseDate:
		return "date"
	case BaseDatetime:
		return "datetime"
	}
	return "?"
}

// ---------------------------------------------------------------------------
// TableColumnType
// ---------------------------------------------------------------------------

// TableColumnType represents a column's SQL type normalized to a base type.
type TableColumnType struct {
	BaseType BaseType
	DBType   string // The raw type string from the database (e.g. "INTEGER", "TEXT")
}

// ---------------------------------------------------------------------------
// TableColumnInfo
// ---------------------------------------------------------------------------

// TableColumnInfo describes a single column of a database table.
type TableColumnInfo struct {
	Name            string
	Type            TableColumnType
	IsPrimaryKey    bool
	IsNotNull       bool
	IsAutoIncrement bool
	IsGeometry      bool
	GeomType        string // e.g. "POINT", "LINESTRING", "POLYGON"
	GeomSrsId       int
	GeomHasZ        bool
	GeomHasM        bool
}

// CompareWithBaseTypes compares two columns using base types (looser than exact match).
// This mirrors the C++ TableColumnInfo::compareWithBaseTypes().
func (c TableColumnInfo) CompareWithBaseTypes(other TableColumnInfo) bool {
	return c.Name == other.Name &&
		c.Type.BaseType == other.Type.BaseType &&
		c.IsPrimaryKey == other.IsPrimaryKey &&
		c.IsNotNull == other.IsNotNull &&
		c.IsAutoIncrement == other.IsAutoIncrement &&
		c.IsGeometry == other.IsGeometry &&
		c.GeomType == other.GeomType &&
		c.GeomSrsId == other.GeomSrsId &&
		c.GeomHasZ == other.GeomHasZ &&
		c.GeomHasM == other.GeomHasM
}

// SetGeometry marks this column as a geometry column with the given parameters.
func (c *TableColumnInfo) SetGeometry(geomTypeName string, srsId int, hasM, hasZ bool) {
	c.Type.BaseType = BaseGeometry
	c.IsGeometry = true
	c.GeomType = geomTypeName
	c.GeomSrsId = srsId
	c.GeomHasM = hasM
	c.GeomHasZ = hasZ
}

// ---------------------------------------------------------------------------
// CrsDefinition
// ---------------------------------------------------------------------------

// CrsDefinition holds coordinate reference system info.
type CrsDefinition struct {
	SrsId    int
	AuthName string // e.g. "EPSG"
	AuthCode int
	Wkt      string // WKT definition
}

// Equal compares two CRS definitions.
// Note: WKT is intentionally excluded from comparison because the format
// may vary even for the same CRS object.
func (c CrsDefinition) Equal(other CrsDefinition) bool {
	return c.SrsId == other.SrsId &&
		c.AuthName == other.AuthName &&
		c.AuthCode == other.AuthCode
}

// ---------------------------------------------------------------------------
// Extent
// ---------------------------------------------------------------------------

// Extent holds a spatial extent (bounding box).
type Extent struct {
	MinX, MinY, MaxX, MaxY float64
}

// ---------------------------------------------------------------------------
// TableSchema
// ---------------------------------------------------------------------------

// TableSchema describes a database table including its columns and CRS.
type TableSchema struct {
	Name    string
	Columns []TableColumnInfo
	Crs     CrsDefinition
}

// HasPrimaryKey returns true if at least one column is part of the primary key.
func (ts TableSchema) HasPrimaryKey() bool {
	for _, col := range ts.Columns {
		if col.IsPrimaryKey {
			return true
		}
	}
	return false
}

// ColumnFromName returns the index of the column with the given name,
// or -1 if no such column exists.
func (ts TableSchema) ColumnFromName(name string) int {
	for i, col := range ts.Columns {
		if col.Name == name {
			return i
		}
	}
	return -1
}

// GeometryColumn returns the index of the first geometry column,
// or -1 if no geometry column exists.
func (ts TableSchema) GeometryColumn() int {
	for i, col := range ts.Columns {
		if col.IsGeometry {
			return i
		}
	}
	return -1
}

// Equal performs an exact comparison of two table schemas (including DB types).
func (ts TableSchema) Equal(other TableSchema) bool {
	if ts.Name != other.Name || !ts.Crs.Equal(other.Crs) || len(ts.Columns) != len(other.Columns) {
		return false
	}
	for i := range ts.Columns {
		a, b := ts.Columns[i], other.Columns[i]
		if a.Name != b.Name ||
			a.Type != b.Type ||
			a.IsPrimaryKey != b.IsPrimaryKey ||
			a.IsNotNull != b.IsNotNull ||
			a.IsAutoIncrement != b.IsAutoIncrement ||
			a.IsGeometry != b.IsGeometry ||
			a.GeomType != b.GeomType ||
			a.GeomSrsId != b.GeomSrsId ||
			a.GeomHasZ != b.GeomHasZ ||
			a.GeomHasM != b.GeomHasM {
			return false
		}
	}
	return true
}

// CompareWithBaseTypes performs a looser comparison using only base types
// (from the C++ compareWithBaseTypes method).
func (ts TableSchema) CompareWithBaseTypes(other TableSchema) bool {
	if ts.Name != other.Name || !ts.Crs.Equal(other.Crs) || len(ts.Columns) != len(other.Columns) {
		return false
	}
	for i := range ts.Columns {
		if !ts.Columns[i].CompareWithBaseTypes(other.Columns[i]) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// ColumnType — convert raw DB type string to base type
// ---------------------------------------------------------------------------

// ColumnType converts a raw column type string (as reported by the database)
// to a TableColumnType with a normalized base type. The driverName determines
// how the type string is parsed ("sqlite" or "postgres").
func ColumnType(logger Logger, columnType string, driverName string, isGeometry bool) TableColumnType {
	switch driverName {
	case DriverSQLite:
		return sqliteToBaseColumn(logger, columnType, isGeometry)
	case DriverPostgres:
		return postgresToBaseColumn(logger, columnType, isGeometry)
	default:
		// In C++ this throws GeoDiffException. We return TEXT as a safe fallback
		// but log an error if a logger is available.
		if logger != nil {
			logger.Error("Unknown driver name " + driverName + " in ColumnType, using text fallback")
		}
		return TableColumnType{BaseType: BaseText, DBType: columnType}
	}
}

// sqliteToBaseColumn maps a SQLite/GPKG column type string to a base type.
func sqliteToBaseColumn(logger Logger, columnType string, isGeometry bool) TableColumnType {
	t := TableColumnType{DBType: columnType}

	if isGeometry {
		t.BaseType = BaseGeometry
		return t
	}

	dbType := strings.ToLower(columnType)

	switch {
	case dbType == "int" || dbType == "integer" || dbType == "smallint" ||
		dbType == "mediumint" || dbType == "bigint" || dbType == "tinyint":
		t.BaseType = BaseInteger

	case dbType == "double" || dbType == "real" || dbType == "double precision" || dbType == "float":
		t.BaseType = BaseDouble

	case dbType == "bool" || dbType == "boolean":
		t.BaseType = BaseBoolean

	case dbType == "text" || strings.HasPrefix(dbType, "text(") || strings.HasPrefix(dbType, "varchar("):
		t.BaseType = BaseText

	case dbType == "blob":
		t.BaseType = BaseBlob

	case dbType == "datetime":
		t.BaseType = BaseDatetime

	case dbType == "date":
		t.BaseType = BaseDate

	default:
		if logger != nil {
			logger.Info("Converting GeoPackage type " + columnType + " to base type unsuccessful, using text.")
		}
		t.BaseType = BaseText
	}

	return t
}

// postgresToBaseColumn maps a PostgreSQL column type string to a base type.
func postgresToBaseColumn(logger Logger, columnType string, isGeometry bool) TableColumnType {
	t := TableColumnType{DBType: columnType}

	if isGeometry {
		t.BaseType = BaseGeometry
		return t
	}

	dbType := strings.ToLower(columnType)

	switch {
	case dbType == "integer" || dbType == "smallint" || dbType == "bigint":
		t.BaseType = BaseInteger

	case dbType == "double precision" || dbType == "real" ||
		strings.HasPrefix(dbType, "numeric") || strings.HasPrefix(dbType, "decimal"):
		t.BaseType = BaseDouble

	case dbType == "boolean":
		t.BaseType = BaseBoolean

	case dbType == "text" || strings.HasPrefix(dbType, "text(") ||
		dbType == "varchar" || strings.HasPrefix(dbType, "varchar(") ||
		dbType == "character varying" || strings.HasPrefix(dbType, "character varying(") ||
		dbType == "char" || strings.HasPrefix(dbType, "char(") || strings.HasPrefix(dbType, "character(") ||
		dbType == "citetext" || dbType == "uuid":
		t.BaseType = BaseText

	case dbType == "bytea":
		t.BaseType = BaseBlob

	case dbType == "timestamp without time zone":
		t.BaseType = BaseDatetime

	case dbType == "date":
		t.BaseType = BaseDate

	default:
		if logger != nil {
			logger.Warn("Converting PostgreSQL type " + columnType + " to base type unsuccessful, using text.")
		}
		t.BaseType = BaseText
	}

	return t
}
