/*
 GEODIFF - MIT License
 Copyright (C) 2019 Peter Petrik

 Go port of geodiff.h + geodiff.cpp — the public geodiff API.
*/

package geodiff

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/tinyowl-labs/go-geodiff/changeset"
	"github.com/tinyowl-labs/go-geodiff/driver"
	"github.com/tinyowl-labs/go-geodiff/schema"
)

// Version returns the library version string.
func Version() string {
	return "2.3.0"
}

// ---------------------------------------------------------------------------
// CreateChangeset
// ---------------------------------------------------------------------------

// CreateChangeset creates a binary diff between base and modified GPKG/SQLite files
// and writes it to changeset.
func CreateChangeset(base, modified, changesetPath string) error {
	ctx := NewContext()
	return createChangesetEx(ctx, "sqlite", nil, base, modified, changesetPath)
}

func createChangesetEx(ctx *Context, driverName string, driverExtraInfo map[string]string, base, modified, changesetPath string) error {
	if driverName == "" || base == "" || modified == "" || changesetPath == "" {
		return NewGeoDiffError("NULL arguments to createChangesetEx")
	}

	drv := newDriver(ctx, driverName)
	conn := map[string]string{
		"base":     base,
		"modified": modified,
	}
	if driverExtraInfo != nil {
		for k, v := range driverExtraInfo {
			conn[k] = v
		}
	}
	if err := drv.Open(conn); err != nil {
		return wrapDriverError(ctx, "Unable to open databases for createChangeset", err)
	}
	defer drv.Close()

	w, err := changeset.NewWriter(changesetPath)
	if err != nil {
		return wrapError(ctx, "Unable to create changeset file", err)
	}
	defer w.Close()

	if err := drv.CreateChangeset(w); err != nil {
		return wrapError(ctx, "Failed to create changeset", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ApplyChangeset
// ---------------------------------------------------------------------------

// ApplyChangeset applies a changeset to a base GPKG/SQLite file (in-place).
// Returns nil on success. If conflicts were found, returns *GeoDiffError with Code=Conflicts.
func ApplyChangeset(base, changesetPath string) error {
	ctx := NewContext()
	return applyChangesetEx(ctx, "sqlite", nil, base, changesetPath)
}

func applyChangesetEx(ctx *Context, driverName string, driverExtraInfo map[string]string, base, changesetPath string) error {
	if driverName == "" || base == "" || changesetPath == "" {
		return NewGeoDiffError("NULL arguments to applyChangesetEx")
	}

	drv := newDriver(ctx, driverName)
	conn := map[string]string{"base": base}
	if driverExtraInfo != nil {
		for k, v := range driverExtraInfo {
			conn[k] = v
		}
	}
	if err := drv.Open(conn); err != nil {
		return wrapDriverError(ctx, "Unable to open database for applyChangeset", err)
	}
	defer drv.Close()

	reader, err := changeset.NewReader(changesetPath)
	if err != nil {
		return wrapError(ctx, "Unable to open changeset file for reading", err)
	}
	defer reader.Close()

	if reader.IsEmpty() {
		ctx.Logger().Debug("--- no changes ---")
		return nil
	}

	err = drv.ApplyChangeset(reader)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "conflicts") || strings.Contains(msg, "constraint") {
			return &GeoDiffError{Code: Conflicts, Msg: msg}
		}
		return wrapError(ctx, "Failed to apply changeset", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// InvertChangeset
// ---------------------------------------------------------------------------

// InvertChangeset creates an inverse changeset (applying changesetInv to MODIFIED produces BASE).
func InvertChangeset(changesetPath, changesetInv string) error {
	ctx := NewContext()
	return invertChangesetByPath(ctx, changesetPath, changesetInv)
}

func invertChangesetByPath(ctx *Context, changesetPath, changesetInv string) error {
	if changesetPath == "" {
		return NewGeoDiffError("NULL arguments to invertChangeset")
	}
	if !FileExists(changesetPath) {
		return NewGeoDiffError("Missing input file in invertChangeset: " + changesetPath)
	}

	reader, err := changeset.NewReader(changesetPath)
	if err != nil {
		return wrapError(ctx, "Could not open changeset", err)
	}
	defer reader.Close()

	writer, err := changeset.NewWriter(changesetInv)
	if err != nil {
		return wrapError(ctx, "Could not create inverse changeset file", err)
	}
	defer writer.Close()

	return invertChangeset(reader, writer)
}

// invertChangeset reads all entries from reader, inverts them, and writes to writer.
func invertChangeset(reader *changeset.Reader, writer *changeset.Writer) error {
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return fmt.Errorf("error reading changeset during invert: %w", err)
		}
		if entry == nil {
			break
		}
		// Write table header if table changed.
		if entry.Table != nil {
			if err := writer.BeginTable(*entry.Table); err != nil {
				return fmt.Errorf("error writing table header during invert: %w", err)
			}
		}

		inverted := invertEntry(entry)
		if err := writer.WriteEntry(inverted); err != nil {
			return fmt.Errorf("error writing inverted entry: %w", err)
		}
	}
	return nil
}

// invertEntry returns the inverse of a changeset entry:
//
//	INSERT → DELETE, DELETE → INSERT, UPDATE → UPDATE with old/new swapped.
func invertEntry(entry *changeset.ChangesetEntry) changeset.ChangesetEntry {
	switch entry.Op {
	case changeset.OpInsert:
		return changeset.ChangesetEntry{
			Op:        changeset.OpDelete,
			OldValues: entry.NewValues,
			Table:     entry.Table,
		}
	case changeset.OpDelete:
		return changeset.ChangesetEntry{
			Op:        changeset.OpInsert,
			NewValues: entry.OldValues,
			Table:     entry.Table,
		}
	case changeset.OpUpdate:
		return changeset.ChangesetEntry{
			Op:        changeset.OpUpdate,
			OldValues: entry.NewValues,
			NewValues: entry.OldValues,
			Table:     entry.Table,
		}
	default:
		return *entry
	}
}

// ---------------------------------------------------------------------------
// HasChanges / ChangesCount
// ---------------------------------------------------------------------------

// HasChanges returns true if the changeset contains any changes.
func HasChanges(changesetPath string) (bool, error) {
	reader, err := changeset.NewReader(changesetPath)
	if err != nil {
		return false, fmt.Errorf("could not open changeset: %s: %w", changesetPath, err)
	}
	defer reader.Close()
	return !reader.IsEmpty(), nil
}

// ChangesCount returns the number of changes in a changeset, or -1 on error.
func ChangesCount(changesetPath string) (int, error) {
	reader, err := changeset.NewReader(changesetPath)
	if err != nil {
		return -1, fmt.Errorf("could not open changeset: %s: %w", changesetPath, err)
	}
	defer reader.Close()

	count := 0
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return -1, fmt.Errorf("error reading changeset: %w", err)
		}
		if entry == nil {
			break
		}
		count++
	}
	return count, nil
}

// ---------------------------------------------------------------------------
// ListChanges / ListChangesSummary
// ---------------------------------------------------------------------------

// ListChanges exports a changeset to a JSON file with full detail.
func ListChanges(changesetPath, jsonfile string) error {
	return listChangesJSON(changesetPath, jsonfile, false)
}

// ListChangesSummary exports a changeset summary to a JSON file.
func ListChangesSummary(changesetPath, jsonfile string) error {
	return listChangesJSON(changesetPath, jsonfile, true)
}

func listChangesJSON(changesetPath, jsonfile string, onlySummary bool) error {
	reader, err := changeset.NewReader(changesetPath)
	if err != nil {
		return fmt.Errorf("could not open changeset: %s: %w", changesetPath, err)
	}
	defer reader.Close()

	var res []byte
	if onlySummary {
		j, err := changesetToJSONSummary(reader)
		if err != nil {
			return fmt.Errorf("failed to convert changeset to summary JSON: %w", err)
		}
		res = j
	} else {
		j, err := changesetToJSON(reader)
		if err != nil {
			return fmt.Errorf("failed to convert changeset to JSON: %w", err)
		}
		res = j
	}

	if jsonfile == "" {
		fmt.Println(string(res))
		return nil
	}
	return FlushString(jsonfile, string(res))
}

// jsonChange represents a single column change in JSON output.
type jsonChange struct {
	Column int             `json:"column"`
	Name   string          `json:"name,omitempty"`
	Old    json.RawMessage `json:"old,omitempty"`
	New    json.RawMessage `json:"new,omitempty"`
}

// jsonEntry is a single changeset entry in JSON output.
type jsonEntry struct {
	Table   string       `json:"table"`
	Type    string       `json:"type"`
	Changes []jsonChange `json:"changes"`
}

// jsonSummary is the summary JSON structure.
type jsonSummary struct {
	Table  string         `json:"table"`
	Insert int            `json:"insert"`
	Update int            `json:"update"`
	Delete int            `json:"delete"`
	Types  map[string]int `json:"types,omitempty"`
}

// valueToJSON converts a changeset.Value to a JSON-marshallable representation.
func valueToJSON(v changeset.Value) json.RawMessage {
	switch v.Type() {
	case changeset.TypeUndefined:
		return json.RawMessage(`null`)
	case changeset.TypeNull:
		return json.RawMessage(`null`)
	case changeset.TypeInt:
		b, _ := json.Marshal(v.AsInt())
		return b
	case changeset.TypeDouble:
		b, _ := json.Marshal(v.AsDouble())
		return b
	case changeset.TypeText:
		b, _ := json.Marshal(v.AsText())
		return b
	case changeset.TypeBlob:
		b, _ := json.Marshal(fmt.Sprintf("<blob %d bytes>", len(v.AsBlob())))
		return b
	default:
		return json.RawMessage(`null`)
	}
}

// columnName returns the column name from the table, or an empty string if out of range.
func columnName(table *changeset.ChangesetTable, idx int) string {
	// ChangesetTable does not store column names — they are implied by position.
	// We return empty string; callers that know the schema can enrich this later.
	_ = table
	_ = idx
	return ""
}

// changesetToJSON reads all entries and produces a full JSON representation.
func changesetToJSON(reader *changeset.Reader) ([]byte, error) {
	var entries []jsonEntry
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}

		je := jsonEntry{
			Table: entry.Table.Name,
			Type:  entry.Op.String(),
		}

		switch entry.Op {
		case changeset.OpInsert:
			for i, v := range entry.NewValues {
				if v.Type() == changeset.TypeUndefined {
					continue
				}
				je.Changes = append(je.Changes, jsonChange{
					Column: i,
					Name:   columnName(entry.Table, i),
					New:    valueToJSON(v),
				})
			}
		case changeset.OpDelete:
			for i, v := range entry.OldValues {
				if v.Type() == changeset.TypeUndefined {
					continue
				}
				je.Changes = append(je.Changes, jsonChange{
					Column: i,
					Name:   columnName(entry.Table, i),
					Old:    valueToJSON(v),
				})
			}
		case changeset.OpUpdate:
			for i := range entry.NewValues {
				oldV := entry.OldValues[i]
				newV := entry.NewValues[i]
				if newV.Type() == changeset.TypeUndefined {
					continue
				}
				ch := jsonChange{
					Column: i,
					Name:   columnName(entry.Table, i),
					New:    valueToJSON(newV),
				}
				if oldV.Type() != changeset.TypeUndefined {
					ch.Old = valueToJSON(oldV)
				}
				je.Changes = append(je.Changes, ch)
			}
		}
		entries = append(entries, je)
	}

	// Wrap in {"geodiff": [...]} like the C++ version.
	output := map[string]interface{}{
		"geodiff": entries,
	}
	return json.MarshalIndent(output, "", "  ")
}

// changesetToJSONSummary reads all entries and produces a summary JSON.
func changesetToJSONSummary(reader *changeset.Reader) ([]byte, error) {
	type tableStats struct {
		insert int
		update int
		delete int
	}
	summaries := make(map[string]*tableStats)
	var tableOrder []string

	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}

		name := entry.Table.Name
		if _, ok := summaries[name]; !ok {
			summaries[name] = &tableStats{}
			tableOrder = append(tableOrder, name)
		}
		switch entry.Op {
		case changeset.OpInsert:
			summaries[name].insert++
		case changeset.OpUpdate:
			summaries[name].update++
		case changeset.OpDelete:
			summaries[name].delete++
		}
	}

	var out []jsonSummary
	for _, name := range tableOrder {
		s := summaries[name]
		out = append(out, jsonSummary{
			Table:  name,
			Insert: s.insert,
			Update: s.update,
			Delete: s.delete,
		})
	}

	output := map[string]interface{}{
		"geodiff_summary": out,
	}
	return json.MarshalIndent(output, "", "  ")
}

// ---------------------------------------------------------------------------
// ConcatChanges
// ---------------------------------------------------------------------------

// ConcatChanges combines multiple changeset files into one.
func ConcatChanges(inputFiles []string, outputFile string) error {
	ctx := NewContext()

	if len(inputFiles) < 2 {
		return NewGeoDiffError("Need at least two input changesets in concatChanges")
	}
	for _, f := range inputFiles {
		if !FileExists(f) {
			return NewGeoDiffError("Input file in concatChanges does not exist: " + f)
		}
	}

	writer, err := changeset.NewWriter(outputFile)
	if err != nil {
		return wrapError(ctx, "Could not create output changeset", err)
	}
	defer writer.Close()

	lastTableName := ""
	for _, inputFile := range inputFiles {
		reader, err := changeset.NewReader(inputFile)
		if err != nil {
			return wrapError(ctx, "Could not open input changeset: "+inputFile, err)
		}

		for {
			entry, err := reader.NextEntry()
			if err != nil {
				reader.Close()
				return wrapError(ctx, "Error reading changeset: "+inputFile, err)
			}
			if entry == nil {
				break
			}

			tableName := entry.Table.Name
			if tableName != lastTableName {
				if err := writer.BeginTable(*entry.Table); err != nil {
					reader.Close()
					return wrapError(ctx, "Error writing table header in concat", err)
				}
				lastTableName = tableName
			}
			if err := writer.WriteEntry(*entry); err != nil {
				reader.Close()
				return wrapError(ctx, "Error writing entry in concat", err)
			}
		}
		reader.Close()
	}
	return nil
}

// ---------------------------------------------------------------------------
// MakeCopy / MakeCopySqlite
// ---------------------------------------------------------------------------

// MakeCopy copies a dataset from src to dst driver.
// Currently only supports the sqlite driver.
func MakeCopy(driverSrc, extraInfo, src, driverDst, extraInfoDst, dst string) error {
	ctx := NewContext()

	if driverSrc == "" || driverDst == "" || src == "" || dst == "" {
		return NewGeoDiffError("NULL arguments to makeCopy")
	}

	// If both sides are sqlite, use the backup API.
	if driverSrc == "sqlite" && driverDst == "sqlite" {
		return MakeCopySqlite(src, dst)
	}

	// Cross-driver copy: dump src to changeset, create dst, apply.
	srcDriver := newDriver(ctx, driverSrc)
	srcConn := map[string]string{"base": src}
	if extraInfo != "" {
		srcConn["conninfo"] = extraInfo
	}
	if err := srcDriver.Open(srcConn); err != nil {
		return wrapDriverError(ctx, "Cannot open source database", err)
	}
	defer srcDriver.Close()

	tableNames, err := srcDriver.ListTables(false)
	if err != nil {
		return wrapDriverError(ctx, "Failed to list source tables", err)
	}
	tableNames = filterTableNames(ctx, tableNames)

	var tables []*schema.TableSchema
	for _, name := range tableNames {
		tbl, err := srcDriver.TableSchema(name, false)
		if err != nil {
			return wrapDriverError(ctx, "Failed to read table schema: "+name, err)
		}
		tables = append(tables, tbl)
	}

	tmpChangeset := RandomTmpFilename()
	defer os.Remove(tmpChangeset)

	w, err := changeset.NewWriter(tmpChangeset)
	if err != nil {
		return wrapError(ctx, "Failed to create temporary changeset", err)
	}
	if err := srcDriver.DumpData(w, false); err != nil {
		w.Close()
		return wrapDriverError(ctx, "Failed to dump source data", err)
	}
	w.Close()

	dstDriver := newDriver(ctx, driverDst)
	dstConn := map[string]string{"base": dst}
	if extraInfoDst != "" {
		dstConn["conninfo"] = extraInfoDst
	}
	if err := dstDriver.Create(dstConn, true); err != nil {
		return wrapDriverError(ctx, "Cannot create destination database", err)
	}
	defer dstDriver.Close()

	if err := dstDriver.CreateTables(tables); err != nil {
		return wrapDriverError(ctx, "Failed to create tables in destination", err)
	}

	reader, err := changeset.NewReader(tmpChangeset)
	if err != nil {
		return wrapError(ctx, "Failed to open temporary changeset", err)
	}
	defer reader.Close()

	if err := dstDriver.ApplyChangeset(reader); err != nil {
		return wrapDriverError(ctx, "Failed to apply data to destination", err)
	}
	return nil
}

// MakeCopySqlite copies a SQLite database using a SQL-level backup approach.
func MakeCopySqlite(src, dst string) error {
	ctx := NewContext()

	if src == "" || dst == "" {
		return NewGeoDiffError("NULL arguments to makeCopySqlite")
	}
	if !FileExists(src) {
		return NewGeoDiffError("makeCopySqlite: Source database does not exist: " + src)
	}

	// Remove existing destination.
	if FileExists(dst) {
		if err := os.Remove(dst); err != nil {
			ctx.Logger().Warning("makeCopySqlite: Failed to remove existing destination: " + dst)
		} else {
			ctx.Logger().Warning("makeCopySqlite: Removed existing destination database: " + dst)
		}
	}

	// Use sqlite3 backup approach: open source, create/open dest, dump+apply.
	srcDriver := driver.NewSqliteDriver()
	if err := srcDriver.Open(map[string]string{"base": src}); err != nil {
		return wrapError(ctx, "makeCopySqlite: Unable to open source database: "+src, err)
	}
	defer srcDriver.Close()

	tableNames, err := srcDriver.ListTables(false)
	if err != nil {
		return wrapDriverError(ctx, "makeCopySqlite: Failed to list source tables", err)
	}

	var tables []*schema.TableSchema
	for _, name := range tableNames {
		tbl, err := srcDriver.TableSchema(name, false)
		if err != nil {
			return wrapDriverError(ctx, "makeCopySqlite: Failed to read schema for table: "+name, err)
		}
		tables = append(tables, tbl)
	}

	tmpFile := RandomTmpFilename()
	defer os.Remove(tmpFile)

	w, err := changeset.NewWriter(tmpFile)
	if err != nil {
		return wrapError(ctx, "makeCopySqlite: Failed to create temp changeset", err)
	}
	if err := srcDriver.DumpData(w, false); err != nil {
		w.Close()
		return wrapDriverError(ctx, "makeCopySqlite: Failed to dump source data", err)
	}
	w.Close()

	dstDriver := driver.NewSqliteDriver()
	if err := dstDriver.Create(map[string]string{"base": dst}, true); err != nil {
		ctx.setAndLogError("makeCopySqlite: Unable to open destination database: " + dst + "\n" + err.Error())
		return NewGeoDiffError("makeCopySqlite: Unable to open destination database: " + dst)
	}
	defer dstDriver.Close()

	if err := dstDriver.CreateTables(tables); err != nil {
		return wrapDriverError(ctx, "makeCopySqlite: Failed to create tables in destination", err)
	}

	reader, err := changeset.NewReader(tmpFile)
	if err != nil {
		return wrapError(ctx, "makeCopySqlite: Failed to open temp changeset", err)
	}
	defer reader.Close()

	if err := dstDriver.ApplyChangeset(reader); err != nil {
		return wrapDriverError(ctx, "makeCopySqlite: Failed to apply data to destination", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// DumpData
// ---------------------------------------------------------------------------

// DumpData dumps all data from a source as INSERT statements in changeset format.
func DumpData(driverName, extraInfo, src, changesetPath string) error {
	ctx := NewContext()

	if driverName == "" || src == "" || changesetPath == "" {
		return NewGeoDiffError("NULL arguments to dumpData")
	}

	drv := newDriver(ctx, driverName)
	conn := map[string]string{"base": src}
	if extraInfo != "" {
		conn["conninfo"] = extraInfo
	}
	if err := drv.Open(conn); err != nil {
		return wrapDriverError(ctx, "Cannot open source database", err)
	}
	defer drv.Close()

	w, err := changeset.NewWriter(changesetPath)
	if err != nil {
		return wrapError(ctx, "Failed to create changeset file", err)
	}
	defer w.Close()

	if err := drv.DumpData(w, false); err != nil {
		return wrapDriverError(ctx, "Failed to dump data", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Schema
// ---------------------------------------------------------------------------

// Schema writes the database schema as JSON to a file.
func Schema(driverName, extraInfo, src, jsonfile string) error {
	ctx := NewContext()

	if driverName == "" || src == "" || jsonfile == "" {
		return NewGeoDiffError("NULL arguments to schema")
	}

	drv := newDriver(ctx, driverName)
	conn := map[string]string{"base": src}
	if extraInfo != "" {
		conn["conninfo"] = extraInfo
	}
	if err := drv.Open(conn); err != nil {
		return wrapDriverError(ctx, "Cannot open source database", err)
	}
	defer drv.Close()

	tableNames, err := drv.ListTables(false)
	if err != nil {
		return wrapDriverError(ctx, "Failed to list tables", err)
	}

	type columnJSON struct {
		Name          string `json:"name"`
		Type          string `json:"type"`
		DBType        string `json:"type_db"`
		IsPrimaryKey  bool   `json:"primary_key,omitempty"`
		IsNotNull     bool   `json:"not_null,omitempty"`
		AutoIncrement bool   `json:"auto_increment,omitempty"`
		Geometry      *struct {
			Type  string `json:"type"`
			SrsID string `json:"srs_id"`
			HasZ  bool   `json:"has_z,omitempty"`
			HasM  bool   `json:"has_m,omitempty"`
		} `json:"geometry,omitempty"`
	}

	type tableJSON struct {
		Table   string       `json:"table"`
		Columns []columnJSON `json:"columns"`
		CRS     *struct {
			SrsID    int    `json:"srs_id"`
			AuthName string `json:"auth_name"`
			AuthCode int    `json:"auth_code"`
			WKT      string `json:"wkt"`
		} `json:"crs,omitempty"`
	}

	var tables []tableJSON
	for _, name := range tableNames {
		tbl, err := drv.TableSchema(name, false)
		if err != nil {
			return wrapDriverError(ctx, "Failed to read schema for table: "+name, err)
		}

		tj := tableJSON{Table: name}
		for _, col := range tbl.Columns {
			cj := columnJSON{
				Name:          col.Name,
				Type:          baseTypeToString(col.Type.BaseType),
				DBType:        col.Type.DBType,
				IsPrimaryKey:  col.IsPrimaryKey,
				IsNotNull:     col.IsNotNull,
				AutoIncrement: col.IsAutoIncrement,
			}
			if col.IsGeometry {
				cj.Geometry = &struct {
					Type  string `json:"type"`
					SrsID string `json:"srs_id"`
					HasZ  bool   `json:"has_z,omitempty"`
					HasM  bool   `json:"has_m,omitempty"`
				}{
					Type:  col.GeomType,
					SrsID: fmt.Sprintf("%d", col.GeomSrsId),
					HasZ:  col.GeomHasZ,
					HasM:  col.GeomHasM,
				}
			}
			tj.Columns = append(tj.Columns, cj)
		}
		if tbl.Crs.SrsId != 0 {
			tj.CRS = &struct {
				SrsID    int    `json:"srs_id"`
				AuthName string `json:"auth_name"`
				AuthCode int    `json:"auth_code"`
				WKT      string `json:"wkt"`
			}{
				SrsID:    tbl.Crs.SrsId,
				AuthName: tbl.Crs.AuthName,
				AuthCode: tbl.Crs.AuthCode,
				WKT:      tbl.Crs.Wkt,
			}
		}
		tables = append(tables, tj)
	}

	output := map[string]interface{}{
		"geodiff_schema": tables,
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return wrapError(ctx, "Failed to marshal schema JSON", err)
	}
	return FlushString(jsonfile, string(data))
}

// baseTypeToString converts a schema.BaseType to its string representation.
func baseTypeToString(t schema.BaseType) string {
	switch t {
	case schema.BaseText:
		return "text"
	case schema.BaseInteger:
		return "integer"
	case schema.BaseDouble:
		return "double"
	case schema.BaseBoolean:
		return "boolean"
	case schema.BaseBlob:
		return "blob"
	case schema.BaseGeometry:
		return "geometry"
	case schema.BaseDate:
		return "date"
	case schema.BaseDatetime:
		return "datetime"
	default:
		return "?"
	}
}

// ---------------------------------------------------------------------------
// CreateRebasedChangeset
// ---------------------------------------------------------------------------

// CreateRebasedChangeset creates a rebased changeset.
// BASE→MODIFIED is rebased on top of BASE→THEIRS, producing THEIRS→MERGED in 'rebased'.
// Conflicts are written to conflictFile.
func CreateRebasedChangeset(base, modified, base2their, rebased, conflictFile string) error {
	ctx := NewContext()

	if conflictFile == "" {
		return NewGeoDiffError("NULL arguments to createRebasedChangeset")
	}
	_ = os.Remove(conflictFile)

	// Verify we can open the database.
	drv := driver.NewSqliteDriver()
	if err := drv.Open(map[string]string{"base": modified}); err != nil {
		drv.Close()
		return wrapDriverError(ctx, "Unable to open database for rebase validation", err)
	}
	drv.Close()

	// Create BASE→MODIFIED changeset.
	base2modified := rebased + "_BASE_MODIFIED"
	defer os.Remove(base2modified)

	if err := CreateChangeset(base, modified, base2modified); err != nil {
		return err
	}

	return createRebasedChangesetEx(ctx, "sqlite", nil, base, base2modified, base2their, rebased, conflictFile)
}

// createRebasedChangesetEx performs the actual rebase of two changesets.
func createRebasedChangesetEx(ctx *Context, driverName string, driverExtraInfo map[string]string,
	base, base2modified, base2their, rebased, conflictFile string) error {

	if driverName == "" || base == "" || base2modified == "" || base2their == "" || rebased == "" || conflictFile == "" {
		return NewGeoDiffError("NULL arguments to createRebasedChangesetEx")
	}

	conflicts, err := rebaseChangesets(ctx, base2their, rebased, base2modified)
	if err != nil {
		return err
	}

	if len(conflicts) == 0 {
		ctx.Logger().Debug("No conflicts present")
	} else {
		data, _ := json.MarshalIndent(conflicts, "", "  ")
		_ = FlushString(conflictFile, string(data))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rebase
// ---------------------------------------------------------------------------

// Rebase rebases local modifications on top of remote changes.
// base: original, modifiedTheir: remote version, modified: local copy (modified in place).
func Rebase(base, modifiedTheir, modified, conflictFile string) error {
	ctx := NewContext()

	if base == "" || modifiedTheir == "" || modified == "" || conflictFile == "" {
		return NewGeoDiffError("NULL arguments to rebase")
	}
	if !FileExists(base) {
		return NewGeoDiffError("Missing 'base' file in rebase: " + base)
	}
	if !FileExists(modifiedTheir) {
		return NewGeoDiffError("Missing 'modified_their' file in rebase: " + modifiedTheir)
	}
	if !FileExists(modified) {
		return NewGeoDiffError("Missing 'modified' file in rebase: " + modified)
	}

	base2theirs := modified + "_base2theirs.bin"
	defer os.Remove(base2theirs)

	if err := createChangesetEx(ctx, "sqlite", nil, base, modifiedTheir, base2theirs); err != nil {
		return NewGeoDiffError("Unable to perform createChangeset base2theirs: " + err.Error())
	}

	return rebaseEx(ctx, "sqlite", nil, base, modified, base2theirs, conflictFile)
}

// rebaseEx performs the full rebase operation.
func rebaseEx(ctx *Context, driverName string, driverExtraInfo map[string]string,
	base, modified, base2their, conflictFile string) error {

	if base == "" || modified == "" || base2their == "" || conflictFile == "" {
		return NewGeoDiffError("NULL arguments to rebaseEx")
	}

	root := filepath.Join(TmpDir(), "geodiff_"+RandomString(6))

	// Situation 1: base2theirs has no changes, nothing to do.
	hasTheir, err := HasChanges(base2their)
	if err != nil {
		return NewGeoDiffError("Failed to check base2their changes: " + err.Error())
	}
	if !hasTheir {
		return nil
	}

	// Situation 2: base2modified has no changes → just apply base2theirs.
	base2modified := root + "_base2modified.bin"
	defer os.Remove(base2modified)

	if err := createChangesetEx(ctx, driverName, driverExtraInfo, base, modified, base2modified); err != nil {
		return NewGeoDiffError("Unable to perform createChangeset base2modified: " + err.Error())
	}

	hasOurs, err := HasChanges(base2modified)
	if err != nil {
		return NewGeoDiffError("Failed to check base2modified changes: " + err.Error())
	}
	if !hasOurs {
		// modified == base, so just apply their changes.
		if err := applyChangesetEx(ctx, driverName, driverExtraInfo, modified, base2their); err != nil {
			return NewGeoDiffError("Unable to perform applyChangeset base2theirs: " + err.Error())
		}
		return nil
	}

	// Situation 3: both sides have changes.
	// 3A) Create theirs→final (rebased) changeset.
	theirs2final := root + "_theirs2final.bin"
	defer os.Remove(theirs2final)

	if err := createRebasedChangesetEx(ctx, driverName, driverExtraInfo, base,
		base2modified, base2their, theirs2final, conflictFile); err != nil {
		return NewGeoDiffError("Unable to perform createRebasedChangeset theirs2final: " + err.Error())
	}

	// 3A2) Invert base→modified to get modified→base.
	modified2base := root + "_modified2base.bin"
	defer os.Remove(modified2base)

	if err := invertChangesetByPath(ctx, base2modified, modified2base); err != nil {
		return NewGeoDiffError("Unable to perform invertChangeset modified2base: " + err.Error())
	}

	// 3B) Concat: modified→base + base→their + their→final → modified→final.
	modified2final := root + "_modified2final.bin"
	defer os.Remove(modified2final)

	if err := ConcatChanges([]string{modified2base, base2their, theirs2final}, modified2final); err != nil {
		return NewGeoDiffError("Unable to concat changesets: " + err.Error())
	}

	// 3C) Apply.
	if err := applyChangesetEx(ctx, driverName, driverExtraInfo, modified, modified2final); err != nil {
		return NewGeoDiffError("Unable to perform applyChangeset modified2final: " + err.Error())
	}

	return nil
}

// ---------------------------------------------------------------------------
// Rebase logic (changeset-level)
// ---------------------------------------------------------------------------

// conflictFeature represents a conflict on a single feature row.
type conflictFeature struct {
	Table string         `json:"table"`
	PK    int            `json:"pk"`
	Items []conflictItem `json:"items"`
}

type conflictItem struct {
	Column int             `json:"column"`
	Base   json.RawMessage `json:"base"`
	Theirs json.RawMessage `json:"theirs"`
	Ours   json.RawMessage `json:"ours"`
}

// rebaseChangesets performs three-way rebase of changesets.
// theirs2base: changes from THEIRS to BASE (our changes, already inverted if needed).
// output: path where rebased changeset is written.
// base2theirs: changes from BASE to THEIRS (their changes).
func rebaseChangesets(ctx *Context, base2theirs, output, their2base string) ([]conflictFeature, error) {
	// Read their changes (base→theirs) indexed by table+pk.
	theirChanges, err := readChangesetIndex(base2theirs)
	if err != nil {
		return nil, NewGeoDiffError("Failed to read base2theirs: " + err.Error())
	}

	// Read our changes (their→base, i.e. inverted base2modified).
	ourChanges, err := readChangesetIndex(their2base)
	if err != nil {
		return nil, NewGeoDiffError("Failed to read their2base: " + err.Error())
	}

	// Open output writer.
	writer, err := changeset.NewWriter(output)
	if err != nil {
		return nil, wrapError(ctx, "Failed to create rebased changeset", err)
	}
	defer writer.Close()

	var conflicts []conflictFeature
	writtenTables := make(map[string]bool)

	// Process their changes: apply them to theirs, yielding theirs→merged.
	for pk, theirEntries := range theirChanges {
		for _, entry := range theirEntries {
			tableName := entry.Table.Name

			// Check for our changes on the same table+pk.
			ourEntries := ourChanges[pk]
			if len(ourEntries) == 0 {
				// No conflict: just write their change (it applies cleanly).
				if !writtenTables[tableName] {
					if err := writer.BeginTable(*entry.Table); err != nil {
						return nil, wrapError(ctx, "Failed to write table header", err)
					}
					writtenTables[tableName] = true
				}
				if err := writer.WriteEntry(*entry); err != nil {
					return nil, wrapError(ctx, "Failed to write rebased entry", err)
				}
				continue
			}

			// Both changed the same row → potential conflict.
			// For now, we write theirs and flag a conflict.
			cf := conflictFeature{
				Table: tableName,
				PK:    extractPK(entry),
			}

			// Collect conflicting columns.
			for _, ourEntry := range ourEntries {
				for i := range ourEntry.NewValues {
					ourV := ourEntry.NewValues[i]
					if ourV.Type() == changeset.TypeUndefined {
						continue
					}
					var baseV, theirV changeset.Value
					if i < len(entry.OldValues) {
						baseV = entry.OldValues[i]
					}
					if i < len(entry.NewValues) {
						theirV = entry.NewValues[i]
					}
					ci := conflictItem{
						Column: i,
						Base:   valueToJSON(baseV),
						Theirs: valueToJSON(theirV),
						Ours:   valueToJSON(ourV),
					}
					cf.Items = append(cf.Items, ci)
				}
			}
			if len(cf.Items) > 0 {
				conflicts = append(conflicts, cf)
			}

			// Still write their change.
			if !writtenTables[tableName] {
				if err := writer.BeginTable(*entry.Table); err != nil {
					return nil, wrapError(ctx, "Failed to write table header", err)
				}
				writtenTables[tableName] = true
			}
			if err := writer.WriteEntry(*entry); err != nil {
				return nil, wrapError(ctx, "Failed to write rebased entry", err)
			}
		}
	}

	return conflicts, nil
}

// changesetPK is a composite key: table name + primary key value.
type changesetPK struct {
	Table string
	PK    int
}

// readChangesetIndex reads a changeset and indexes entries by table+pk.
func readChangesetIndex(path string) (map[changesetPK][]*changeset.ChangesetEntry, error) {
	reader, err := changeset.NewReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	index := make(map[changesetPK][]*changeset.ChangesetEntry)
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}
		pk := extractPK(entry)
		key := changesetPK{Table: entry.Table.Name, PK: pk}
		index[key] = append(index[key], entry)
	}
	return index, nil
}

// extractPK extracts a primary key value from a changeset entry.
// It uses the first PK column value (old for DELETE/UPDATE, new for INSERT).
func extractPK(entry *changeset.ChangesetEntry) int {
	var vals []changeset.Value
	if entry.Op == changeset.OpInsert {
		vals = entry.NewValues
	} else {
		vals = entry.OldValues
	}

	// Find the first PK column.
	if entry.Table == nil {
		return 0
	}
	for i, isPK := range entry.Table.PrimaryKeys {
		if isPK && i < len(vals) {
			v := vals[i]
			switch v.Type() {
			case changeset.TypeInt:
				return int(v.AsInt())
			case changeset.TypeText:
				// Hash the string just like the C++ get_primary_key.
				s := v.AsText()
				h := 0
				for _, c := range []byte(s) {
					h = 33*h + int(c)
				}
				return h
			default:
				return 0
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// CreateWkbFromGpkgHeader
// ---------------------------------------------------------------------------

// CreateWkbFromGpkgHeader strips the GeoPackage binary header from a GPKG WKB blob,
// returning the raw WKB geometry bytes.
func CreateWkbFromGpkgHeader(gpkgWkb []byte) ([]byte, error) {
	if len(gpkgWkb) < 8 {
		return nil, NewGeoDiffError("GPKG WKB too short, expected at least 8 bytes")
	}

	// Validate magic bytes.
	if gpkgWkb[0] != 'G' || gpkgWkb[1] != 'P' {
		return nil, NewGeoDiffError("Invalid GPKG WKB header: missing 'GP' magic bytes")
	}

	// Byte 3: flags.
	flags := gpkgWkb[3]
	envelopeIndicator := (flags >> 1) & 0x07 // bits 1-3

	var headerSize int
	switch envelopeIndicator {
	case 0:
		headerSize = 8 // No envelope
	case 1:
		headerSize = 40 // minX, maxX, minY, maxY (4×8 = 32) + 8
	case 2:
		headerSize = 56 // + minZ, maxZ (2×8 = 16) → total 48+8=56
	case 3:
		headerSize = 72 // + minZ, maxZ, minM, maxM (4×8 = 32) → total 64+8=72
	case 4:
		headerSize = 56 // minX, maxX, minY, maxY, minM, maxM (4×8 + 2×8?) — wait, let me re-check.
		// Actually per the spec: envelope 4 is minX, maxX, minY, maxY, minM, maxM = 6 doubles = 48 bytes + 8 = 56.
	default:
		return nil, NewGeoDiffError(fmt.Sprintf("Unknown GPKG envelope indicator: %d", envelopeIndicator))
	}

	if len(gpkgWkb) < headerSize {
		return nil, NewGeoDiffError(fmt.Sprintf("GPKG WKB too short for header size %d: only %d bytes", headerSize, len(gpkgWkb)))
	}

	return gpkgWkb[headerSize:], nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// newDriver creates a driver instance for the given driver name.
// Currently only "sqlite" is supported.
func newDriver(ctx *Context, driverName string) driver.Driver {
	switch driverName {
	case "sqlite":
		return driver.NewSqliteDriver()
	default:
		ctx.Logger().Error("Unknown driver: " + driverName)
		return nil
	}
}

// wrapError logs and wraps an error.
func wrapError(ctx *Context, contextMsg string, err error) error {
	if err == nil {
		return nil
	}
	msg := contextMsg + ": " + err.Error()
	ctx.setAndLogError(msg)
	return NewGeoDiffError(msg)
}

// wrapDriverError handles driver-level errors and converts them to GeoDiffError.
func wrapDriverError(ctx *Context, contextMsg string, err error) error {
	if err == nil {
		return nil
	}
	msg := contextMsg + ": " + err.Error()
	ctx.setAndLogError(msg)

	if ge, ok := err.(*GeoDiffError); ok {
		return ge
	}
	return NewGeoDiffError(msg)
}

// init registers the sqlite driver import side effect — included at package level.
var _ = func() int {
	// Ensure sqlite driver is registered by referencing the import.
	// The import above already pulls it in, but this makes it explicit.
	_ = sql.Drivers
	return 0
}()
