// Package driver implements the geodiff driver interface for SQLite/GeoPackage.
// Ported from sqlitedriver.cpp, sqlitedriver.h, and driver.h in the C++ geodiff library.
package driver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/tinyowl-labs/go-geodiff/changeset"
	"github.com/tinyowl-labs/go-geodiff/schema"
)

// Side indicates which database to query (base or modified).
type Side int

const (
	BaseSide     Side = iota // base (original) database
	ModifiedSide             // modified database
)

// quoteIdent returns a properly quoted SQLite identifier.
// It wraps the identifier in double quotes and doubles any embedded double quotes.
func quoteIdent(s string) string {
	q := `"`
	return q + strings.ReplaceAll(s, q, q+q) + q
}

// ChangeApplyResult mirrors the C++ ChangeApplyResult enum.
type ChangeApplyResult int

const (
	ApplyApplied ChangeApplyResult = iota
	ApplySkipped
	ApplyConstraintConflict
	ApplyNoChange
)

// Connector manages database connection lifecycle.
type Connector interface {
	Open(ctx context.Context, conn map[string]string) error
	Create(ctx context.Context, conn map[string]string, overwrite bool) error
	Close() error
}

// SchemaReader reads database table schemas.
type SchemaReader interface {
	ListTables(ctx context.Context, side Side) ([]string, error)
	TableSchema(ctx context.Context, tableName string, side Side) (*schema.TableSchema, error)
}

// DiffWriter creates changesets by comparing two databases.
type DiffWriter interface {
	CreateChangeset(ctx context.Context, writer *changeset.Writer) error
}

// DiffApplier applies changesets to a database.
type DiffApplier interface {
	ApplyChangeset(ctx context.Context, reader *changeset.Reader) error
	CreateTables(ctx context.Context, tables []*schema.TableSchema) error
	DumpData(ctx context.Context, writer *changeset.Writer, side Side) error
}

// Driver is the full interface for all geodiff backends.
// It composes Connector, SchemaReader, DiffWriter, and DiffApplier.
type Driver interface {
	Connector
	SchemaReader
	DiffWriter
	DiffApplier
}

// SqliteDriver implements Driver for SQLite databases (including GeoPackage).
type SqliteDriver struct {
	db          *sql.DB
	hasModified bool
}

// NewSqliteDriver creates a new SqliteDriver.
func NewSqliteDriver() *SqliteDriver {
	return &SqliteDriver{}
}

func (d *SqliteDriver) databaseName(side Side) (string, error) {
	if d.hasModified {
		if side == ModifiedSide {
			return "main", nil
		}
		return "aux", nil
	}
	if side == ModifiedSide {
		return "", fmt.Errorf("'modified' database not open")
	}
	return "main", nil
}

// Open opens a SQLite database and optionally attaches a second as "aux".
func (d *SqliteDriver) Open(ctx context.Context, conn map[string]string) error {
	_ = ctx
	base, ok := conn["base"]
	if !ok {
		return fmt.Errorf("missing 'base' file")
	}
	if !fileExistsCheck(base) {
		return fmt.Errorf("missing 'base' file when opening sqlite driver: %s", base)
	}

	modified, hasModified := conn["modified"]
	d.hasModified = hasModified

	var dbPath string
	if d.hasModified {
		if !fileExistsCheck(modified) {
			return fmt.Errorf("missing 'modified' file when opening sqlite driver: %s", modified)
		}
		dbPath = modified
	} else {
		dbPath = base
	}

	var err error
	d.db, err = sql.Open("sqlite", dbPath+"?mode=rw")
	if err != nil {
		return fmt.Errorf("unable to open %s as sqlite3 database: %w", dbPath, err)
	}

	if d.hasModified {
		attachSQL := fmt.Sprintf("ATTACH DATABASE '%s' AS aux", strings.ReplaceAll(base, "'", "''"))
		if _, err := d.db.ExecContext(context.Background(), attachSQL); err != nil {
			d.db.Close()
			return fmt.Errorf("unable to attach database: %w", err)
		}
	}

	if _, err := d.db.ExecContext(context.Background(), "PRAGMA foreign_keys = 1"); err != nil {
		d.db.Close()
		return fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	return nil
}

// Create creates a new SQLite database file.
func (d *SqliteDriver) Create(ctx context.Context, conn map[string]string, overwrite bool) error {
	_ = ctx
	base, ok := conn["base"]
	if !ok {
		return fmt.Errorf("missing 'base' file")
	}
	if overwrite {
		os.Remove(base)
	}
	if fileExistsCheck(base) {
		return fmt.Errorf("unable to create sqlite3 database - already exists: %s", base)
	}
	var err error
	d.db, err = sql.Open("sqlite", base)
	if err != nil {
		return fmt.Errorf("unable to create %s as sqlite3 database: %w", base, err)
	}
	// Force file creation by executing a statement
	if _, err := d.db.ExecContext(context.Background(), "SELECT 1"); err != nil {
		d.db.Close()
		return fmt.Errorf("unable to initialize %s: %w", base, err)
	}
	return nil
}

// Close closes the database connection.
func (d *SqliteDriver) Close() error {
	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// ListTables returns all user table names (excluding GPKG system tables).
func (d *SqliteDriver) ListTables(ctx context.Context, side Side) ([]string, error) {
	dbName, err := d.databaseName(side)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`SELECT name FROM %s.sqlite_master
		WHERE type='table' AND sql NOT LIKE 'CREATE VIRTUAL%%%%'
		ORDER BY name`, dbName)

	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list SQLite tables: %w", err)
	}
	defer rows.Close()

	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "gpkg_") {
			continue
		}
		if strings.HasPrefix(name, "rtree_") {
			continue
		}
		if name == "sqlite_sequence" {
			continue
		}
		tableNames = append(tableNames, name)
	}
	return tableNames, rows.Err()
}

func (d *SqliteDriver) tableExists(tableName, dbName string) (bool, error) {
	query := fmt.Sprintf("SELECT name FROM %s.sqlite_master WHERE type='table' AND name='%s'",
		dbName, strings.ReplaceAll(tableName, "'", "''"))
	rows, err := d.db.QueryContext(context.Background(), query)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	return rows.Next(), rows.Err()
}

// TableSchema returns the table schema for the given table.
func (d *SqliteDriver) TableSchema(ctx context.Context, tableName string, side Side) (*schema.TableSchema, error) {
	dbName, err := d.databaseName(side)
	if err != nil {
		return nil, err
	}

	exists, err := d.tableExists(tableName, dbName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("table does not exist: %s", tableName)
	}

	tbl := &schema.TableSchema{Name: tableName}

	pragmaQuery := fmt.Sprintf("PRAGMA '%s'.table_info('%s')",
		dbName, strings.ReplaceAll(tableName, "'", "''"))
	rows, err := d.db.QueryContext(ctx, pragmaQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to get table info for %s: %w", tableName, err)
	}

	columnTypes := make(map[string]string)
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dfltValue *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return nil, fmt.Errorf("failed to scan column info: %w", err)
		}
		if name == "" {
			rows.Close()
			return nil, fmt.Errorf("NULL column name in table schema: %s", tableName)
		}
		colInfo := schema.TableColumnInfo{
			Name:         name,
			IsNotNull:    notNull != 0,
			IsPrimaryKey: pk != 0,
		}
		columnTypes[name] = colType
		tbl.Columns = append(tbl.Columns, colInfo)
	}
	rows.Close()

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error for table %s: %w", tableName, err)
	}

	// Check if gpkg_geometry_columns table is present
	hasGeomCols, _ := d.tableExists("gpkg_geometry_columns", dbName)
	if hasGeomCols {
		srsId := -1
		geomQuery := fmt.Sprintf("SELECT * FROM %s.gpkg_geometry_columns WHERE table_name = '%s'",
			dbName, strings.ReplaceAll(tableName, "'", "''"))
		geomRows, gErr := d.db.QueryContext(ctx, geomQuery)
		if gErr == nil {
			for geomRows.Next() {
				var gTableName, colName, geomTypeName string
				var gSrsId int
				var hasZ, hasM int
				if err := geomRows.Scan(&gTableName, &colName, &geomTypeName, &gSrsId, &hasZ, &hasM); err != nil {
					geomRows.Close()
					return nil, fmt.Errorf("failed to scan geometry column: %w", err)
				}
				if colName == "" {
					geomRows.Close()
					return nil, fmt.Errorf("NULL column name in gpkg_geometry_columns: %s", tableName)
				}
				if geomTypeName == "" {
					geomRows.Close()
					return nil, fmt.Errorf("NULL type name in gpkg_geometry_columns: %s", tableName)
				}
				srsId = gSrsId
				idx := tbl.ColumnFromName(colName)
				if idx < 0 {
					geomRows.Close()
					return nil, fmt.Errorf("inconsistent entry in gpkg_geometry_columns - geometry column not found: %s", colName)
				}
				tbl.Columns[idx].SetGeometry(geomTypeName, srsId, hasM != 0, hasZ != 0)
			}
			geomRows.Close()
			if err := geomRows.Err(); err != nil {
				return nil, fmt.Errorf("geomRows iteration error for table %s: %w", tableName, err)
			}
		}

		if srsId != -1 {
			crsQuery := fmt.Sprintf("SELECT * FROM %s.gpkg_spatial_ref_sys WHERE srs_id = %d", dbName, srsId)
			crsRows, cErr := d.db.QueryContext(ctx, crsQuery)
			if cErr != nil {
				return nil, fmt.Errorf("failed to query gpkg_spatial_ref_sys: %w", cErr)
			}
			if !crsRows.Next() {
				crsRows.Close()
				return nil, fmt.Errorf("unable to find entry in gpkg_spatial_ref_sys for srs_id = %d", srsId)
			}
			var srsName, orgName, definition, description string
			var srsID2, orgCoordsysID int
			if err := crsRows.Scan(&srsName, &srsID2, &orgName, &orgCoordsysID, &definition, &description); err != nil {
				crsRows.Close()
				return nil, fmt.Errorf("failed to scan CRS: %w", err)
			}
			crsRows.Close()
			if orgName == "" {
				return nil, fmt.Errorf("NULL auth name in gpkg_spatial_ref_sys: %s", tableName)
			}
			if definition == "" {
				return nil, fmt.Errorf("NULL definition in gpkg_spatial_ref_sys: %s", tableName)
			}
			tbl.Crs = schema.CrsDefinition{
				SrsId:    srsId,
				AuthName: orgName,
				AuthCode: orgCoordsysID,
				Wkt:      definition,
			}
		}
	}

	// Update column types
	for i := range tbl.Columns {
		col := &tbl.Columns[i]
		dbType := columnTypes[col.Name]
		col.Type = schema.ColumnType(nil, dbType, schema.DriverSQLite, col.IsGeometry)
		if col.IsPrimaryKey && strings.EqualFold(dbType, "integer") {
			col.IsAutoIncrement = true
		}
	}

	return tbl, nil
}

// --- Changeset creation ---

func sqlFindInserted(tableName string, tbl *schema.TableSchema, reverse bool) string {
	qTable := quoteIdent(tableName)
	var exprPk strings.Builder
	for _, c := range tbl.Columns {
		if c.IsPrimaryKey {
			if exprPk.Len() > 0 {
				exprPk.WriteString(" AND ")
			}
			qCol := quoteIdent(c.Name)
			fmt.Fprintf(&exprPk, "\"main\".%s.%s=\"aux\".%s.%s",
				qTable, qCol, qTable, qCol)
		}
	}
	fromDB, otherDB := "main", "aux"
	if reverse {
		fromDB, otherDB = otherDB, fromDB
	}
	return fmt.Sprintf("SELECT * FROM \"%s\".%s WHERE NOT EXISTS (SELECT 1 FROM \"%s\".%s WHERE %s)",
		fromDB, qTable, otherDB, qTable, exprPk.String())
}

func sqlFindModified(tableName string, tbl *schema.TableSchema) string {
	qTable := quoteIdent(tableName)
	var exprPk, exprOther strings.Builder
	for _, c := range tbl.Columns {
		qCol := quoteIdent(c.Name)
		if c.IsPrimaryKey {
			if exprPk.Len() > 0 {
				exprPk.WriteString(" AND ")
			}
			fmt.Fprintf(&exprPk, "\"main\".%s.%s=\"aux\".%s.%s",
				qTable, qCol, qTable, qCol)
		} else {
			if exprOther.Len() > 0 {
				exprOther.WriteString(" OR ")
			}
			fmt.Fprintf(&exprOther, "\"main\".%s.%s IS NOT \"aux\".%s.%s",
				qTable, qCol, qTable, qCol)
		}
	}
	if exprOther.Len() == 0 {
		return fmt.Sprintf("SELECT * FROM \"main\".%s, \"aux\".%s WHERE %s",
			qTable, qTable, exprPk.String())
	}
	return fmt.Sprintf("SELECT * FROM \"main\".%s, \"aux\".%s WHERE %s AND (%s)",
		qTable, qTable, exprPk.String(), exprOther.String())
}

func changesetValue(val any) changeset.Value {
	if val == nil {
		return changeset.NewValueNull()
	}
	switch v := val.(type) {
	case int64:
		return changeset.NewValueInt(v)
	case float64:
		return changeset.NewValueDouble(v)
	case string:
		return changeset.NewValueText(v)
	case []byte:
		b := make([]byte, len(v))
		copy(b, v)
		return changeset.NewValueBlob(b)
	default:
		return changeset.NewValueNull()
	}
}

func schemaToChangesetTable(tableName string, tbl *schema.TableSchema) changeset.ChangesetTable {
	pks := make([]bool, len(tbl.Columns))
	for i, c := range tbl.Columns {
		pks[i] = c.IsPrimaryKey
	}
	return changeset.ChangesetTable{Name: tableName, PrimaryKeys: pks}
}

func scanRowValues(rows *sql.Rows, numColumns int) ([]changeset.Value, error) {
	vals := make([]any, numColumns)
	ptrs := make([]any, numColumns)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	result := make([]changeset.Value, numColumns)
	for i, v := range vals {
		result[i] = changesetValue(v)
	}
	return result, nil
}

func handleInserted(db *sql.DB, tableName string, tbl *schema.TableSchema, reverse bool,
	writer *changeset.Writer, first *bool) error {

	sqlStr := sqlFindInserted(tableName, tbl, reverse)
	rows, err := db.QueryContext(context.Background(), sqlStr)
	if err != nil {
		return fmt.Errorf("failed to query inserted rows for %s: %w", tableName, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		if *first {
			if err := writer.BeginTable(schemaToChangesetTable(tableName, tbl)); err != nil {
				return err
			}
			*first = false
		}
		vals, err := scanRowValues(rows, len(cols))
		if err != nil {
			return fmt.Errorf("failed to scan row for %s: %w", tableName, err)
		}
		entry := changeset.ChangesetEntry{}
		if reverse {
			entry.Op = changeset.OpDelete
			entry.OldValues = vals
		} else {
			entry.Op = changeset.OpInsert
			entry.NewValues = vals
		}
		if err := writer.WriteEntry(entry); err != nil {
			return err
		}
	}
	return rows.Err()
}

func valuesEqual(a, b changeset.Value) bool {
	return a.Equal(b)
}

func checkDatetimeDiff(db *sql.DB, v1, v2 changeset.Value) bool {
	query := "SELECT STRFTIME('%Y-%m-%d %H:%M:%f', ?1) IS NOT STRFTIME('%Y-%m-%d %H:%M:%f', ?2)"
	var s1, s2 any
	if v1.Type() == changeset.TypeNull {
		s1 = nil
	} else if v1.Type() == changeset.TypeText {
		s1, _ = v1.AsText()
	} else {
		return true
	}
	if v2.Type() == changeset.TypeNull {
		s2 = nil
	} else if v2.Type() == changeset.TypeText {
		s2, _ = v2.AsText()
	} else {
		return true
	}
	var result int
	if err := db.QueryRowContext(context.Background(), query, s1, s2).Scan(&result); err != nil {
		return true
	}
	return result != 0
}

func handleUpdated(db *sql.DB, tableName string, tbl *schema.TableSchema,
	writer *changeset.Writer, first *bool) error {

	sqlStr := sqlFindModified(tableName, tbl)
	rows, err := db.QueryContext(context.Background(), sqlStr)
	if err != nil {
		return fmt.Errorf("failed to query modified rows for %s: %w", tableName, err)
	}
	defer rows.Close()

	halfCols := len(tbl.Columns)

	for rows.Next() {
		vals, err := scanRowValues(rows, halfCols*2)
		if err != nil {
			return fmt.Errorf("failed to scan modified row for %s: %w", tableName, err)
		}

		// SQL: SELECT * FROM main.X, aux.X WHERE ...
		// main cols first: vals[0:halfCols], aux cols second: vals[halfCols:]
		entry := changeset.ChangesetEntry{Op: changeset.OpUpdate}
		hasUpdates := false

		for i := 0; i < halfCols; i++ {
			vOld := vals[i+halfCols]
			vNew := vals[i]
			pkey := tbl.Columns[i].IsPrimaryKey
			updated := !valuesEqual(vOld, vNew)

			if updated && tbl.Columns[i].Type.BaseType == schema.BaseDatetime {
				updated = checkDatetimeDiff(db, vOld, vNew)
			}

			if updated {
				hasUpdates = true
			}

			if pkey || updated {
				entry.OldValues = append(entry.OldValues, vOld)
			} else {
				entry.OldValues = append(entry.OldValues, changeset.NewValueUndefined())
			}
			if updated {
				entry.NewValues = append(entry.NewValues, vNew)
			} else {
				entry.NewValues = append(entry.NewValues, changeset.NewValueUndefined())
			}
		}

		if hasUpdates {
			if *first {
				if err := writer.BeginTable(schemaToChangesetTable(tableName, tbl)); err != nil {
					return err
				}
				*first = false
			}
			if err := writer.WriteEntry(entry); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

// CreateChangeset writes all changes between base and modified databases.
func (d *SqliteDriver) CreateChangeset(ctx context.Context, writer *changeset.Writer) error {
	_ = ctx
	if !d.hasModified {
		return fmt.Errorf("cannot create changeset without modified database")
	}
	tablesBase, err := d.ListTables(ctx, BaseSide)
	if err != nil {
		return fmt.Errorf("failed to list base tables: %w", err)
	}
	tablesModified, err := d.ListTables(ctx, ModifiedSide)
	if err != nil {
		return fmt.Errorf("failed to list modified tables: %w", err)
	}
	if !slices.Equal(tablesBase, tablesModified) {
		return fmt.Errorf("table names are not matching between the input databases.\nBase:     %s\nModified: %s",
			strings.Join(tablesBase, ", "), strings.Join(tablesModified, ", "))
	}
	for _, tableName := range tablesBase {
		tbl, err := d.TableSchema(ctx, tableName, BaseSide)
		if err != nil {
			return err
		}
		tblNew, err := d.TableSchema(ctx, tableName, ModifiedSide)
		if err != nil {
			return err
		}
		if !tbl.Equal(*tblNew) && !tbl.CompareWithBaseTypes(*tblNew) {
			return fmt.Errorf("GeoPackage Table schemas are not the same for table: %s", tableName)
		}
		if !tbl.HasPrimaryKey() {
			continue
		}
		first := true
		if err := handleInserted(d.db, tableName, tbl, false, writer, &first); err != nil {
			return err
		}
		if err := handleInserted(d.db, tableName, tbl, true, writer, &first); err != nil {
			return err
		}
		if err := handleUpdated(d.db, tableName, tbl, writer, &first); err != nil {
			return err
		}
	}
	return nil
}

// --- Changeset application ---

func (d *SqliteDriver) applyInsert(tableName string, tbl *schema.TableSchema, entry *changeset.ChangesetEntry) (ChangeApplyResult, error) {
	var cols, placeholders strings.Builder
	args := make([]any, 0, len(tbl.Columns))
	for i, c := range tbl.Columns {
		if i > 0 {
			cols.WriteString(", ")
			placeholders.WriteString(", ")
		}
		cols.WriteString(quoteIdent(c.Name))
		placeholders.WriteString("?")
		args = append(args, convertValueToInterface(entry.NewValues[i]))
	}

	sqlStr := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", quoteIdent(tableName), cols.String(), placeholders.String())
	result, err := d.db.ExecContext(context.Background(), sqlStr, args...)
	if err != nil {
		if isConstraintError(err) {
			return ApplyConstraintConflict, nil
		}
		return ApplyNoChange, fmt.Errorf("SQLite error in INSERT: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return ApplyNoChange, fmt.Errorf("nothing inserted (this should never happen)")
	}
	return ApplyApplied, nil
}

func (d *SqliteDriver) applyUpdate(tableName string, tbl *schema.TableSchema, entry *changeset.ChangesetEntry) (ChangeApplyResult, error) {
	var sets, where strings.Builder
	args := make([]any, 0, len(tbl.Columns)*2)

	for i, c := range tbl.Columns {
		vNew := entry.NewValues[i]
		if vNew.Type() != changeset.TypeUndefined {
			if sets.Len() > 0 {
				sets.WriteString(", ")
			}
			fmt.Fprintf(&sets, "%s = ?", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vNew))
		}
	}

	if sets.Len() == 0 {
		return ApplyNoChange, nil
	}

	for i, c := range tbl.Columns {
		vOld := entry.OldValues[i]
		if c.IsPrimaryKey {
			// PK columns are always required.
			if where.Len() > 0 {
				where.WriteString(" AND ")
			}
			fmt.Fprintf(&where, "%s = ?", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vOld))
		} else if vOld.Type() == changeset.TypeUndefined {
			// Skip undefined columns — they should not be part of the WHERE clause.
			continue
		} else if c.Type.BaseType == schema.BaseDatetime {
			if where.Len() > 0 {
				where.WriteString(" AND ")
			}
			fmt.Fprintf(&where, "STRFTIME('%%Y-%%m-%%d %%H:%%M:%%f', %s) IS STRFTIME('%%Y-%%m-%%d %%H:%%M:%%f', ?)", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vOld))
		} else {
			if where.Len() > 0 {
				where.WriteString(" AND ")
			}
			fmt.Fprintf(&where, "%s IS ?", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vOld))
		}
	}

	sqlStr := fmt.Sprintf("UPDATE %s SET %s WHERE %s", quoteIdent(tableName), sets.String(), where.String())
	result, err := d.db.ExecContext(context.Background(), sqlStr, args...)
	if err != nil {
		if isConstraintError(err) {
			return ApplyConstraintConflict, nil
		}
		return ApplyNoChange, fmt.Errorf("SQLite error in UPDATE: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ApplyNoChange, nil
	}
	return ApplyApplied, nil
}

func (d *SqliteDriver) applyDelete(tableName string, tbl *schema.TableSchema, entry *changeset.ChangesetEntry) (ChangeApplyResult, error) {
	var where strings.Builder
	args := make([]any, 0, len(tbl.Columns))

	for i, c := range tbl.Columns {
		vOld := entry.OldValues[i]
		if c.IsPrimaryKey {
			if where.Len() > 0 {
				where.WriteString(" AND ")
			}
			fmt.Fprintf(&where, "%s = ?", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vOld))
		} else if vOld.Type() == changeset.TypeUndefined {
			continue
		} else if c.Type.BaseType == schema.BaseDatetime {
			if where.Len() > 0 {
				where.WriteString(" AND ")
			}
			fmt.Fprintf(&where, "STRFTIME('%%Y-%%m-%%d %%H:%%M:%%f', %s) IS STRFTIME('%%Y-%%m-%%d %%H:%%M:%%f', ?)", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vOld))
		} else {
			if where.Len() > 0 {
				where.WriteString(" AND ")
			}
			fmt.Fprintf(&where, "%s IS ?", quoteIdent(c.Name))
			args = append(args, convertValueToInterface(vOld))
		}
	}

	sqlStr := fmt.Sprintf("DELETE FROM %s WHERE %s", quoteIdent(tableName), where.String())
	result, err := d.db.ExecContext(context.Background(), sqlStr, args...)
	if err != nil {
		if isConstraintError(err) {
			return ApplyConstraintConflict, nil
		}
		return ApplyNoChange, fmt.Errorf("SQLite error in DELETE: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ApplyNoChange, nil
	}
	return ApplyApplied, nil
}

func (d *SqliteDriver) applyChange(ctx context.Context, state map[string]*schema.TableSchema, entry *changeset.ChangesetEntry) (ChangeApplyResult, error) {
	tableName := entry.Table.Name

	if strings.HasPrefix(tableName, "gpkg_") {
		return ApplySkipped, nil
	}

	tbl, ok := state[tableName]
	if !ok {
		schemaTbl, err := d.TableSchema(ctx, tableName, BaseSide)
		if err != nil {
			return ApplyNoChange, err
		}
		if len(schemaTbl.Columns) == 0 {
			return ApplyNoChange, fmt.Errorf("no such table: %s", tableName)
		}
		if len(schemaTbl.Columns) != len(entry.Table.PrimaryKeys) {
			return ApplyNoChange, fmt.Errorf("wrong number of columns for table: %s", tableName)
		}
		for i := 0; i < len(entry.Table.PrimaryKeys); i++ {
			if schemaTbl.Columns[i].IsPrimaryKey != entry.Table.PrimaryKeys[i] {
				return ApplyNoChange, fmt.Errorf("mismatch of primary keys in table: %s", tableName)
			}
		}
		state[tableName] = schemaTbl
		tbl = schemaTbl
	}

	switch entry.Op {
	case changeset.OpInsert:
		return d.applyInsert(tableName, tbl, entry)
	case changeset.OpUpdate:
		return d.applyUpdate(tableName, tbl, entry)
	case changeset.OpDelete:
		return d.applyDelete(tableName, tbl, entry)
	default:
		return ApplyNoChange, fmt.Errorf("unexpected operation")
	}
}

func convertValueToInterface(v changeset.Value) any {
	switch v.Type() {
	case changeset.TypeUndefined:
		return nil
	case changeset.TypeNull:
		return nil
	case changeset.TypeInt:
		n, _ := v.AsInt()
		return n
	case changeset.TypeDouble:
		f, _ := v.AsDouble()
		return f
	case changeset.TypeText:
		s, _ := v.AsText()
		return s
	case changeset.TypeBlob:
		b, _ := v.AsBlob()
		return b
	default:
		return nil
	}
}

func isConstraintError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint") ||
		strings.Contains(msg, "FOREIGN KEY constraint") ||
		strings.Contains(msg, "NOT NULL constraint") ||
		strings.Contains(msg, "CHECK constraint") ||
		strings.Contains(msg, "PRIMARY KEY constraint")
}

// ApplyChangeset reads a changeset and applies it to the database.
func (d *SqliteDriver) ApplyChangeset(ctx context.Context, reader *changeset.Reader) error {
	_ = ctx
	if _, err := d.db.ExecContext(ctx, "SAVEPOINT changeset_apply"); err != nil {
		return fmt.Errorf("unable to start savepoint transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			d.db.ExecContext(context.Background(), "ROLLBACK TO changeset_apply")
			d.db.ExecContext(context.Background(), "RELEASE changeset_apply")
		}
	}()

	if _, err := d.db.ExecContext(ctx, "PRAGMA defer_foreign_keys = 1"); err != nil {
		return fmt.Errorf("failed to defer foreign key checks: %w", err)
	}

	triggerNames, triggerCmds, err := sqliteTriggers(d.db)
	if err != nil {
		return err
	}
	if err := dropTriggers(d.db, triggerNames); err != nil {
		return err
	}

	unrecoverableConflicts := 0
	var conflictingEntries []changeset.ChangesetEntry
	state := make(map[string]*schema.TableSchema)
	tableCopies := make(map[string]*changeset.ChangesetTable)

	for {
		entry, err := reader.NextEntry()
		if err != nil {
			createTriggers(d.db, triggerCmds)
			return err
		}
		if entry == nil {
			break // EOF
		}

		res, err := d.applyChange(ctx, state, entry)
		if err != nil {
			createTriggers(d.db, triggerCmds)
			return err
		}
		switch res {
		case ApplyApplied, ApplySkipped:
		case ApplyConstraintConflict:
			if _, ok := tableCopies[entry.Table.Name]; !ok {
				tableCopies[entry.Table.Name] = entry.Table.Clone()
			}
			cloned := *entry.Clone()
			cloned.Table = *tableCopies[entry.Table.Name]
			conflictingEntries = append(conflictingEntries, cloned)
		case ApplyNoChange:
			unrecoverableConflicts++
		}
	}

	// Retry conflicting entries
	var newConflicting []changeset.ChangesetEntry
	for len(conflictingEntries) > 0 {
		for _, centry := range conflictingEntries {
			res, err := d.applyChange(ctx, state, &centry)
			if err != nil {
				createTriggers(d.db, triggerCmds)
				return err
			}
			switch res {
			case ApplyApplied, ApplySkipped:
			case ApplyConstraintConflict:
				newConflicting = append(newConflicting, centry)
			case ApplyNoChange:
				unrecoverableConflicts++
			}
		}
		if len(newConflicting) == len(conflictingEntries) {
			createTriggers(d.db, triggerCmds)
			return fmt.Errorf("could not resolve dependencies in constraint conflicts")
		}
		conflictingEntries = newConflicting
		newConflicting = nil
	}

	if err := createTriggers(d.db, triggerCmds); err != nil {
		return err
	}

	if unrecoverableConflicts > 0 {
		return fmt.Errorf("conflicts encountered while applying changes! Total %d", unrecoverableConflicts)
	}

	if _, err := d.db.ExecContext(ctx, "RELEASE changeset_apply"); err != nil {
		return fmt.Errorf("failed to release savepoint: %w", err)
	}
	committed = true
	return nil
}

// initSpatialMetadata creates the GPKG metadata tables if they don't exist.
func (d *SqliteDriver) initSpatialMetadata() error {
	// Try the GPKG extension function first
	rows, err := d.db.QueryContext(context.Background(), "SELECT InitSpatialMetadata('main');")
	if err == nil {
		rows.Close()
		return nil
	}

	// Fallback: create GeoPackage metadata tables manually
	metadataDDL := []string{
		`CREATE TABLE IF NOT EXISTS gpkg_spatial_ref_sys (
			srs_name TEXT NOT NULL,
			srs_id INTEGER NOT NULL PRIMARY KEY,
			organization TEXT NOT NULL,
			organization_coordsys_id INTEGER NOT NULL,
			definition TEXT NOT NULL,
			description TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS gpkg_contents (
			table_name TEXT NOT NULL PRIMARY KEY,
			data_type TEXT NOT NULL,
			identifier TEXT UNIQUE,
			description TEXT DEFAULT '',
			last_change DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			min_x DOUBLE,
			min_y DOUBLE,
			max_x DOUBLE,
			max_y DOUBLE,
			srs_id INTEGER,
			CONSTRAINT fk_gc_r_srs_id FOREIGN KEY (srs_id) REFERENCES gpkg_spatial_ref_sys(srs_id)
		)`,
		`CREATE TABLE IF NOT EXISTS gpkg_geometry_columns (
			table_name TEXT NOT NULL,
			column_name TEXT NOT NULL,
			geometry_type_name TEXT NOT NULL,
			srs_id INTEGER NOT NULL,
			z TINYINT NOT NULL,
			m TINYINT NOT NULL,
			CONSTRAINT pk_geom_cols PRIMARY KEY (table_name, column_name),
			CONSTRAINT fk_gc_tn FOREIGN KEY (table_name) REFERENCES gpkg_contents(table_name),
			CONSTRAINT fk_gc_srs FOREIGN KEY (srs_id) REFERENCES gpkg_spatial_ref_sys(srs_id)
		)`,
		`CREATE TABLE IF NOT EXISTS gpkg_tile_matrix_set (
			table_name TEXT NOT NULL PRIMARY KEY,
			srs_id INTEGER NOT NULL,
			min_x DOUBLE NOT NULL,
			min_y DOUBLE NOT NULL,
			max_x DOUBLE NOT NULL,
			max_y DOUBLE NOT NULL,
			CONSTRAINT fk_gtms_srs FOREIGN KEY (srs_id) REFERENCES gpkg_spatial_ref_sys(srs_id)
		)`,
		`CREATE TABLE IF NOT EXISTS gpkg_tile_matrix (
			table_name TEXT NOT NULL,
			zoom_level INTEGER NOT NULL,
			matrix_width INTEGER NOT NULL,
			matrix_height INTEGER NOT NULL,
			tile_width INTEGER NOT NULL,
			tile_height INTEGER NOT NULL,
			pixel_x_size DOUBLE NOT NULL,
			pixel_y_size DOUBLE NOT NULL,
			CONSTRAINT pk_ttm PRIMARY KEY (table_name, zoom_level),
			CONSTRAINT fk_tm_tms FOREIGN KEY (table_name) REFERENCES gpkg_tile_matrix_set(table_name)
		)`,
	}

	for _, ddl := range metadataDDL {
		if _, err := d.db.ExecContext(context.Background(), ddl); err != nil {
			return fmt.Errorf("failure initializing spatial metadata: %w", err)
		}
	}
	return nil
}

// CreateTables creates empty tables from schemas.
func (d *SqliteDriver) CreateTables(ctx context.Context, tables []*schema.TableSchema) error {
	_ = ctx
	if err := d.initSpatialMetadata(); err != nil {
		return err
	}

	for _, tbl := range tables {
		if strings.HasPrefix(tbl.Name, "gpkg_") {
			continue
		}
		if tbl.GeometryColumn() >= 0 {
			if err := addGpkgCrsDefinition(d.db, &tbl.Crs); err != nil {
				return err
			}
			if err := addGpkgSpatialTable(d.db, tbl); err != nil {
				return err
			}
		}

		var columns, pkeys strings.Builder
		for _, c := range tbl.Columns {
			if columns.Len() > 0 {
				columns.WriteString(", ")
			}
			fmt.Fprintf(&columns, "%s %s", quoteIdent(c.Name), c.Type.DBType)
			if c.IsNotNull {
				columns.WriteString(" NOT NULL")
			}
			if c.IsPrimaryKey {
				if pkeys.Len() > 0 {
					pkeys.WriteString(", ")
				}
				pkeys.WriteString(quoteIdent(c.Name))
			}
		}

		sqlStr := fmt.Sprintf("CREATE TABLE \"main\".%s (", quoteIdent(tbl.Name))
		if columns.Len() > 0 {
			sqlStr += columns.String()
		}
		if pkeys.Len() > 0 {
			sqlStr += ", PRIMARY KEY (" + pkeys.String() + ")"
		}
		sqlStr += ");"

		if _, err := d.db.ExecContext(ctx, sqlStr); err != nil {
			return fmt.Errorf("failure creating table: %s: %w", tbl.Name, err)
		}
	}
	return nil
}

func addGpkgCrsDefinition(db *sql.DB, crs *schema.CrsDefinition) error {
	var count int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM gpkg_spatial_ref_sys WHERE srs_id = ?", crs.SrsId).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	srsName := fmt.Sprintf("%s:%d", crs.AuthName, crs.AuthCode)
	_, err := db.ExecContext(context.Background(), "INSERT INTO gpkg_spatial_ref_sys VALUES (?, ?, ?, ?, ?, '')",
		srsName, crs.SrsId, crs.AuthName, crs.AuthCode, crs.Wkt)
	if err != nil {
		return fmt.Errorf("failed to insert CRS to gpkg_spatial_ref_sys table: %w", err)
	}
	return nil
}

func addGpkgSpatialTable(db *sql.DB, tbl *schema.TableSchema) error {
	geomIdx := tbl.GeometryColumn()
	if geomIdx < 0 {
		return fmt.Errorf("adding non-spatial tables is not supported: %s", tbl.Name)
	}
	col := tbl.Columns[geomIdx]

	_, err := db.ExecContext(context.Background(),
		"INSERT INTO gpkg_contents (table_name, data_type, identifier, min_x, min_y, max_x, max_y, srs_id) VALUES (?, 'features', ?, 0, 0, 0, 0, ?)",
		tbl.Name, tbl.Name, col.GeomSrsId)
	if err != nil {
		return fmt.Errorf("failed to insert row to gpkg_contents table: %w", err)
	}

	hasZ, hasM := 0, 0
	if col.GeomHasZ {
		hasZ = 1
	}
	if col.GeomHasM {
		hasM = 1
	}
	_, err = db.ExecContext(context.Background(), "INSERT INTO gpkg_geometry_columns VALUES (?, ?, ?, ?, ?, ?)",
		tbl.Name, col.Name, col.GeomType, col.GeomSrsId, hasZ, hasM)
	if err != nil {
		return fmt.Errorf("failed to insert row to gpkg_geometry_columns table: %w", err)
	}
	return nil
}

// DumpData writes all rows from a database as INSERT operations.
func (d *SqliteDriver) DumpData(ctx context.Context, writer *changeset.Writer, side Side) error {
	dbName, err := d.databaseName(side)
	if err != nil {
		return err
	}
	tables, err := d.ListTables(ctx, side)
	if err != nil {
		return err
	}
	for _, tableName := range tables {
		tbl, err := d.TableSchema(ctx, tableName, side)
		if err != nil {
			return err
		}
		if !tbl.HasPrimaryKey() {
			continue
		}
		first := true
		query := fmt.Sprintf("SELECT * FROM \"%s\".%s", dbName, quoteIdent(tableName))
		rows, err := d.db.QueryContext(ctx, query)
		if err != nil {
			return fmt.Errorf("failure dumping changeset: %w", err)
		}
		for rows.Next() {
			if first {
				if err := writer.BeginTable(schemaToChangesetTable(tableName, tbl)); err != nil {
					rows.Close()
					return err
				}
				first = false
			}
			vals, err := scanRowValues(rows, len(tbl.Columns))
			if err != nil {
				rows.Close()
				return fmt.Errorf("failure dumping changeset: %w", err)
			}
			if err := writer.WriteEntry(changeset.ChangesetEntry{
				Op:        changeset.OpInsert,
				NewValues: vals,
			}); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("failure dumping changeset: %w", err)
		}
	}
	return nil
}

func fileExistsCheck(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
