/*
GEODIFF - MIT License
Copyright (C) 2019 Peter Petrik

Go port of geodiffrebase.cpp — the 3-way merge/rebase engine.
*/

package driver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tinyowl-labs/go-geodiff/changeset"
)

// ---------------------------------------------------------------------------
// Conflict types
// ---------------------------------------------------------------------------

// ConflictItem represents a single column-level conflict detected during rebase.
type ConflictItem struct {
	Column int             `json:"column"`
	Base   changeset.Value `json:"-"`
	Theirs changeset.Value `json:"-"`
	Ours   changeset.Value `json:"-"`
}

// ConflictFeature represents a row-level conflict detected during rebase.
type ConflictFeature struct {
	PK        int            `json:"-"`
	TableName string         `json:"-"`
	Items     []ConflictItem `json:"-"`
}

// IsValid returns true if this conflict feature contains at least one item.
func (cf ConflictFeature) IsValid() bool { return len(cf.Items) > 0 }

// ---------------------------------------------------------------------------
// Internal types for tracking rebase state
// ---------------------------------------------------------------------------

// tableRebaseInfo tracks the state of a single table extracted from the
// "theirs" changeset.
type tableRebaseInfo struct {
	inserted map[int]bool              // set of PKs that were inserted
	deleted  map[int]bool              // set of PKs that were deleted
	updated  map[int][]changeset.Value // PK → new column values
}

// databaseRebaseInfo tracks the state of all tables from the "theirs" changeset.
type databaseRebaseInfo struct {
	tables map[string]*tableRebaseInfo
}

// rebaseMapping tracks how primary keys need to be remapped in the rebased changeset.
type rebaseMapping struct {
	// table name → old PK → new PK
	mapIds map[string]map[int]int

	// table name → set of insert PKs that haven't been remapped yet
	// (important because our mapping could cause FID conflicts with PKs
	// that weren't previously in conflict, e.g. if 4,5,6 get mapped
	// 4→6, 5→7 then the original 6 will need to be remapped too: 6→8)
	unmappedInsertIds map[string]map[int]bool
}

const invalidFID = -1 // special PK value for deleted rows

func newRebaseMapping() *rebaseMapping {
	return &rebaseMapping{
		mapIds:            make(map[string]map[int]int),
		unmappedInsertIds: make(map[string]map[int]bool),
	}
}

func (m *rebaseMapping) addPkeyMapping(table string, oldPK, newPK int) {
	ids, ok := m.mapIds[table]
	if !ok {
		ids = make(map[int]int)
		m.mapIds[table] = ids
	}
	ids[oldPK] = newPK
}

func (m *rebaseMapping) hasOldPkey(table string, pk int) bool {
	ids, ok := m.mapIds[table]
	if !ok {
		return false
	}
	_, exists := ids[pk]
	return exists
}

func (m *rebaseMapping) getNewPkey(table string, pk int) (int, error) {
	ids, ok := m.mapIds[table]
	if !ok {
		return 0, fmt.Errorf("internal error: getNewPkey for unknown table %s", table)
	}
	newPK, ok := ids[pk]
	if !ok {
		return 0, fmt.Errorf("internal error: getNewPkey for pk %d in table %s", pk, table)
	}
	return newPK, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// errNoPrimaryKey is returned when a changeset entry lacks a primary key column.
var errNoPrimaryKey = fmt.Errorf("entry has no primary key column")

// getPrimaryKey extracts the primary key value from a changeset entry.
// For INSERT, it reads from newValues; for DELETE/UPDATE, from oldValues.
func getPrimaryKey(entry *changeset.ChangesetEntry) (int, error) {
	for i, isPK := range entry.Table.PrimaryKeys {
		if isPK {
			if entry.Op == changeset.OpInsert {
				n, _ := entry.NewValues[i].AsInt()
				return int(n), nil
			}
			n, _ := entry.OldValues[i].AsInt()
			return int(n), nil
		}
	}
	return 0, errNoPrimaryKey
}

// primaryKeyColumn returns the index of the first primary key column.
func primaryKeyColumn(entry *changeset.ChangesetEntry) (int, error) {
	for i, isPK := range entry.Table.PrimaryKeys {
		if isPK {
			return i, nil
		}
	}
	return -1, errNoPrimaryKey
}

// fileBytesEmpty returns true if the file does not exist or is empty.
func fileBytesEmpty(filename string) bool {
	data, err := os.ReadFile(filename)
	if err != nil {
		return true
	}
	return len(data) == 0
}

// copyFile copies a file from src to dst.
func copyFileContents(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("copyFileContents read: %w", err)
	}
	return os.WriteFile(dst, data, 0644)
}

// ---------------------------------------------------------------------------
// Step 1: Parse the "theirs" changeset into databaseRebaseInfo
// ---------------------------------------------------------------------------

func parseOldChangeset(reader *changeset.Reader, dbInfo *databaseRebaseInfo) error {
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return fmt.Errorf("parseOldChangeset: %w", err)
		}
		if entry == nil {
			break
		}

		tableName := entry.Table.Name
		tableInfo, ok := dbInfo.tables[tableName]
		if !ok {
			tableInfo = &tableRebaseInfo{
				inserted: make(map[int]bool),
				deleted:  make(map[int]bool),
				updated:  make(map[int][]changeset.Value),
			}
			dbInfo.tables[tableName] = tableInfo
		}

		pk, err := getPrimaryKey(entry)
		if err != nil {
			return fmt.Errorf("parseOldChangeset: %w", err)
		}

		switch entry.Op {
		case changeset.OpInsert:
			tableInfo.inserted[pk] = true
		case changeset.OpDelete:
			tableInfo.deleted[pk] = true
		case changeset.OpUpdate:
			tableInfo.updated[pk] = entry.NewValues
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 2: Build RebaseMapping from "ours" changeset
// ---------------------------------------------------------------------------

func findMappingForNewChangeset(
	reader *changeset.Reader,
	dbInfo *databaseRebaseInfo,
	mapping *rebaseMapping,
	freeIndices map[string]int,
) error {
	// Calculate initial free indices: max(theirs.inserted) + 1 per table
	for tableName, tableInfo := range dbInfo.tables {
		maxPK := 0
		for pk := range tableInfo.inserted {
			if pk > maxPK {
				maxPK = pk
			}
		}
		if maxPK > 0 || len(tableInfo.inserted) > 0 {
			freeIndices[tableName] = maxPK + 1
		}
	}

	// First pass: detect conflicts
	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return fmt.Errorf("findMappingForNewChangeset: %w", err)
		}
		if entry == nil {
			break
		}

		tableName := entry.Table.Name
		tableInfo, ok := dbInfo.tables[tableName]
		if !ok {
			continue // table not in theirs records, no rebasing needed
		}

		switch entry.Op {
		case changeset.OpInsert:
			pk, err := getPrimaryKey(entry)
			if err != nil {
				return fmt.Errorf("findMappingForNewChangeset: %w", err)
			}
			if tableInfo.inserted[pk] {
				// Both theirs and ours inserted the same PK → conflict
				freeIdx, exists := freeIndices[tableName]
				if !exists {
					return fmt.Errorf("internal error: freeIndices missing for %s", tableName)
				}
				mapping.addPkeyMapping(tableName, pk, freeIdx)
				freeIndices[tableName] = freeIdx + 1
			} else {
				// Keep track of unmapped inserts for later conflict resolution
				mappedSet, exists := mapping.unmappedInsertIds[tableName]
				if !exists {
					mappedSet = make(map[int]bool)
					mapping.unmappedInsertIds[tableName] = mappedSet
				}
				mappedSet[pk] = true
			}

		case changeset.OpUpdate:
			pk, err := getPrimaryKey(entry)
			if err != nil {
				return fmt.Errorf("findMappingForNewChangeset: %w", err)
			}
			if tableInfo.deleted[pk] {
				// Update on deleted feature
				mapping.addPkeyMapping(tableName, pk, invalidFID)
			}

		case changeset.OpDelete:
			pk, err := getPrimaryKey(entry)
			if err != nil {
				return fmt.Errorf("findMappingForNewChangeset: %w", err)
			}
			if tableInfo.deleted[pk] {
				// Delete of deleted feature
				mapping.addPkeyMapping(tableName, pk, invalidFID)
			}
		}
	}

	// Finalize: if unmapped insert PKs conflict with remapped PKs, remap those too
	for tableName, unmappedSet := range mapping.unmappedInsertIds {
		// Build set of all new PKs from mappings
		usedNewPKs := make(map[int]bool)
		if tableMappings, ok := mapping.mapIds[tableName]; ok {
			for _, newPK := range tableMappings {
				if newPK != invalidFID {
					usedNewPKs[newPK] = true
				}
			}
		}

		for pk := range unmappedSet {
			if usedNewPKs[pk] {
				// Our mapping has introduced a conflict in IDs → remap this old PK too
				freeIdx, exists := freeIndices[tableName]
				if !exists {
					return fmt.Errorf("internal error: freeIndices missing (2) for %s", tableName)
				}
				mapping.addPkeyMapping(tableName, pk, freeIdx)
				usedNewPKs[freeIdx] = true
				freeIndices[tableName] = freeIdx + 1
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Step 3: Entry handlers for producing rebased changeset
// ---------------------------------------------------------------------------

func handleInsert(
	entry *changeset.ChangesetEntry,
	mapping *rebaseMapping,
	outEntry *changeset.ChangesetEntry,
) (bool, error) {
	numColumns := entry.Table.ColumnCount()
	outEntry.Op = changeset.OpInsert
	outEntry.NewValues = make([]changeset.Value, numColumns)

	pk, err := getPrimaryKey(entry)
	if err != nil {
		return false, err
	}
	newPK := pk

	if mapping.hasOldPkey(entry.Table.Name, pk) {
		var mapErr error
		newPK, mapErr = mapping.getNewPkey(entry.Table.Name, pk)
		if mapErr != nil {
			return false, mapErr
		}
	}

	for i := 0; i < numColumns; i++ {
		if entry.Table.PrimaryKeys[i] {
			outEntry.NewValues[i] = changeset.NewValueInt(int64(newPK))
		} else {
			outEntry.NewValues[i] = entry.NewValues[i]
		}
	}
	return true, nil
}

func handleDelete(
	entry *changeset.ChangesetEntry,
	mapping *rebaseMapping,
	tableInfo *tableRebaseInfo,
	outEntry *changeset.ChangesetEntry,
) (bool, error) {
	numColumns := entry.Table.ColumnCount()
	outEntry.Op = changeset.OpDelete
	outEntry.OldValues = make([]changeset.Value, numColumns)

	pk, err := getPrimaryKey(entry)
	if err != nil {
		return false, err
	}
	newPK := pk

	if mapping.hasOldPkey(entry.Table.Name, pk) {
		var mapErr error
		newPK, mapErr = mapping.getNewPkey(entry.Table.Name, pk)
		if mapErr != nil {
			return false, mapErr
		}
		// Both deleted: skip
		if newPK == invalidFID {
			return false, nil
		}
	}

	// Find previously new values (from theirs update) to use as base
	patchedVals, wasUpdated := tableInfo.updated[pk]
	if !wasUpdated {
		patchedVals = make([]changeset.Value, numColumns)
	}

	for i := 0; i < numColumns; i++ {
		if entry.Table.PrimaryKeys[i] {
			outEntry.OldValues[i] = changeset.NewValueInt(int64(newPK))
		} else {
			patchedVal := patchedVals[i]
			if !patchedVal.IsUndefined() {
				outEntry.OldValues[i] = patchedVal
			} else {
				outEntry.OldValues[i] = entry.OldValues[i]
			}
		}
	}
	return true, nil
}

func addConflictItem(cf *ConflictFeature, col int, base, theirs, ours changeset.Value) {
	// Column 4 of gpkg_contents is the last_change timestamp — not a conflict
	if cf.TableName == "gpkg_contents" && col == 4 {
		return
	}
	cf.Items = append(cf.Items, ConflictItem{
		Column: col,
		Base:   base,
		Theirs: theirs,
		Ours:   ours,
	})
}

func handleUpdate(
	entry *changeset.ChangesetEntry,
	mapping *rebaseMapping,
	tableInfo *tableRebaseInfo,
	outEntry *changeset.ChangesetEntry,
	conflicts *[]ConflictFeature,
) (bool, error) {
	numColumns := entry.Table.ColumnCount()
	outEntry.Op = changeset.OpUpdate
	outEntry.OldValues = make([]changeset.Value, numColumns)
	outEntry.NewValues = make([]changeset.Value, numColumns)

	pk, err := getPrimaryKey(entry)
	if err != nil {
		return false, err
	}

	// Check if this update conflicts with a theirs delete
	if mapping.hasOldPkey(entry.Table.Name, pk) {
		newPK, mapErr := mapping.getNewPkey(entry.Table.Name, pk)
		if mapErr != nil {
			return false, mapErr
		}
		if newPK == invalidFID {
			// Our UPDATE conflicts with their DELETE: delete wins, record conflict
			cf := ConflictFeature{PK: pk, TableName: entry.Table.Name}
			for i := 0; i < numColumns; i++ {
				if !entry.NewValues[i].IsUndefined() {
					addConflictItem(&cf, i, entry.OldValues[i], changeset.NewValueUndefined(), entry.NewValues[i])
				}
			}
			if cf.IsValid() {
				*conflicts = append(*conflicts, cf)
			}
			return false, nil
		}
	}

	// Find previously new values from theirs update (will be used as old values in rebased version)
	patchedVals, wasUpdated := tableInfo.updated[pk]
	if !wasUpdated {
		patchedVals = make([]changeset.Value, numColumns)
	}

	cf := ConflictFeature{PK: pk, TableName: entry.Table.Name}
	entryHasChanges := false

	for i := 0; i < numColumns; i++ {
		patchedVal := patchedVals[i]
		if !patchedVal.IsUndefined() && !entry.NewValues[i].IsUndefined() {
			if patchedVal.Equal(entry.NewValues[i]) {
				// Both theirs and ours modified to the same value → no change
				outEntry.OldValues[i] = changeset.NewValueUndefined()
				outEntry.NewValues[i] = changeset.NewValueUndefined()
			} else {
				// Edit conflict: both modified the same column to different values.
				// Don't write either value to the changeset — flag conflict only.
				// The row stays at theirs value; caller must resolve via conflict JSON.
				outEntry.OldValues[i] = changeset.NewValueUndefined()
				outEntry.NewValues[i] = changeset.NewValueUndefined()
				addConflictItem(&cf, i, entry.OldValues[i], patchedVal, entry.NewValues[i])
			}
		} else if !patchedVal.IsUndefined() {
			// Theirs modified this column, ours didn't.
			// Don't include in rebased output — theirs is already applied.
			outEntry.OldValues[i] = changeset.NewValueUndefined()
			outEntry.NewValues[i] = changeset.NewValueUndefined()
		} else {
			// Unchanged by theirs → pass through ours
			outEntry.OldValues[i] = entry.OldValues[i]
			outEntry.NewValues[i] = entry.NewValues[i]
			if !entry.NewValues[i].IsUndefined() {
				entryHasChanges = true
			}
		}
	}

	if cf.IsValid() {
		*conflicts = append(*conflicts, cf)
	}

	return entryHasChanges, nil
}

// ---------------------------------------------------------------------------
// Step 3 driver: produce rebased changeset file
// ---------------------------------------------------------------------------

func prepareNewChangeset(
	reader *changeset.Reader,
	output string,
	mapping *rebaseMapping,
	dbInfo *databaseRebaseInfo,
	conflicts *[]ConflictFeature,
) error {
	type tableDef struct {
		table   changeset.ChangesetTable
		entries []changeset.ChangesetEntry
	}

	tableDefinitions := make(map[string]*changeset.ChangesetTable)
	tableChanges := make(map[string][]changeset.ChangesetEntry)
	var tableOrder []string // preserve insertion order

	for {
		entry, err := reader.NextEntry()
		if err != nil {
			return fmt.Errorf("prepareNewChangeset: %w", err)
		}
		if entry == nil {
			break
		}

		tableName := entry.Table.Name

		if _, exists := tableDefinitions[tableName]; !exists {
			t := entry.Table // copy
			tableDefinitions[tableName] = &t
			tableOrder = append(tableOrder, tableName)
		}

		tableIt, inDBInfo := dbInfo.tables[tableName]
		if !inDBInfo {
			// Table not modified by theirs → copy entry as-is
			tableChanges[tableName] = append(tableChanges[tableName], *entry)
			continue
		}

		writeEntry := false
		var outEntry changeset.ChangesetEntry

		switch entry.Op {
		case changeset.OpUpdate:
			var handleErr error
			writeEntry, handleErr = handleUpdate(entry, mapping, tableIt, &outEntry, conflicts)
			if handleErr != nil {
				return fmt.Errorf("prepareNewChangeset: handleUpdate: %w", handleErr)
			}
		case changeset.OpInsert:
			var handleErr error
			writeEntry, handleErr = handleInsert(entry, mapping, &outEntry)
			if handleErr != nil {
				return fmt.Errorf("prepareNewChangeset: handleInsert: %w", handleErr)
			}
		case changeset.OpDelete:
			var handleErr error
			writeEntry, handleErr = handleDelete(entry, mapping, tableIt, &outEntry)
			if handleErr != nil {
				return fmt.Errorf("prepareNewChangeset: handleDelete: %w", handleErr)
			}
		}

		if writeEntry {
			outEntry.Table = entry.Table
			tableChanges[tableName] = append(tableChanges[tableName], outEntry)
		}
	}

	writer, err := changeset.NewWriter(output)
	if err != nil {
		return fmt.Errorf("prepareNewChangeset: create writer: %w", err)
	}
	defer writer.Close()

	for _, tableName := range tableOrder {
		changes, ok := tableChanges[tableName]
		if !ok || len(changes) == 0 {
			continue
		}

		def := tableDefinitions[tableName]
		if err := writer.BeginTable(*def); err != nil {
			return fmt.Errorf("prepareNewChangeset: beginTable %s: %w", tableName, err)
		}
		for i := range changes {
			if err := writer.WriteEntry(changes[i]); err != nil {
				return fmt.Errorf("prepareNewChangeset: writeEntry: %w", err)
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Rebase — produce THEIRS→MERGED changeset
// ---------------------------------------------------------------------------

// Rebase takes BASE→THEIRS and BASE→OURS changeset files (both on disk)
// and produces a THEIRS→MERGED changeset. Returns any conflicts found.
func Rebase(
	baseTheirs string, // path to BASE→THEIRS changeset
	baseOurs string, // path to BASE→OURS changeset
	theirsMerged string, // path to write THEIRS→MERGED changeset
) ([]ConflictFeature, error) {
	// Remove output file
	os.Remove(theirsMerged)

	// Open BASE→THEIRS
	readerTheirs, err := changeset.NewReader(baseTheirs)
	if err != nil {
		return nil, fmt.Errorf("Rebase: open baseTheirs: %w", err)
	}
	defer readerTheirs.Close()

	if readerTheirs.IsEmpty() {
		// No theirs changes → ours is already the rebased version
		if err := copyFileContents(baseOurs, theirsMerged); err != nil {
			return nil, fmt.Errorf("Rebase: copy baseOurs to theirsMerged: %w", err)
		}
		return nil, nil
	}

	// Open BASE→OURS
	readerOurs, err := changeset.NewReader(baseOurs)
	if err != nil {
		return nil, fmt.Errorf("Rebase: open baseOurs: %w", err)
	}
	defer readerOurs.Close()

	if readerOurs.IsEmpty() {
		// No ours changes → theirs is the rebased version
		if err := copyFileContents(baseTheirs, theirsMerged); err != nil {
			return nil, fmt.Errorf("Rebase: copy baseTheirs to theirsMerged: %w", err)
		}
		return nil, nil
	}

	// Step 1: Parse theirs changeset
	dbInfo := &databaseRebaseInfo{
		tables: make(map[string]*tableRebaseInfo),
	}
	if err := parseOldChangeset(readerTheirs, dbInfo); err != nil {
		return nil, fmt.Errorf("Rebase: parse theirs: %w", err)
	}

	// Step 2: Build mapping from ours changeset
	mapping := newRebaseMapping()
	freeIndices := make(map[string]int)
	if err := findMappingForNewChangeset(readerOurs, dbInfo, mapping, freeIndices); err != nil {
		return nil, fmt.Errorf("Rebase: find mapping: %w", err)
	}

	// Rewind ours reader for step 3
	readerOurs.Rewind()

	// Step 3: Produce rebased changeset
	var conflicts []ConflictFeature
	if err := prepareNewChangeset(readerOurs, theirsMerged, mapping, dbInfo, &conflicts); err != nil {
		return nil, fmt.Errorf("Rebase: prepare changeset: %w", err)
	}

	return conflicts, nil
}

// ---------------------------------------------------------------------------
// Invert changeset
// ---------------------------------------------------------------------------

// InvertChangeset inverts a changeset: INSERT↔DELETE, UPDATE rows are swapped.
// Writes the result to outputPath.
func InvertChangeset(inputPath, outputPath string) error {
	reader, err := changeset.NewReader(inputPath)
	if err != nil {
		return fmt.Errorf("InvertChangeset: %w", err)
	}
	defer reader.Close()
	writer, err := changeset.NewWriter(outputPath)
	if err != nil {
		return fmt.Errorf("InvertChangeset: %w", err)
	}
	defer writer.Close()
	return changeset.InvertChangeset(reader, writer)
}

// ---------------------------------------------------------------------------
// RebaseDirect — in-place rebase of a GPKG file
// ---------------------------------------------------------------------------

// RebaseDirect rebases the OURS GPKG directly (in-place modification).
//
//	base    — path to original BASE GPKG file
//	theirs  — path to THEIRS GPKG file (remote version)
//	ours    — path to OURS GPKG file (local copy, modified in place)
//	conflictFile — path to write conflict JSON (only written if conflicts exist)
//
// Algorithm:
//  1. Undo local changes: create BASE→OURS changeset, invert it, apply to OURS → OURS becomes BASE
//  2. Create BASE→THEIRS changeset, apply to OURS → OURS becomes THEIRS
//  3. Rebase local changes on top: create THEIRS→MERGED changeset, apply to OURS
//  4. Write conflicts to conflictFile if any
func RebaseDirect(
	base string,
	theirs string,
	ours string,
	conflictFile string,
) error {
	// --- Step 0: create temp directory for intermediate files ---
	tmpDir, err := os.MkdirTemp("", "go-geodiff-rebase-")
	if err != nil {
		return fmt.Errorf("RebaseDirect: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	baseOurs := tmpDir + "/base2ours.bin"
	oursBase := tmpDir + "/ours2base.bin"
	baseTheirs := tmpDir + "/base2theirs.bin"
	theirsMerged := tmpDir + "/theirs2merged.bin"

	// --- Step 1: Undo local changes (OURS → BASE) ---

	// 1a. Create BASE→OURS changeset
	d1 := NewSqliteDriver()
	if err := d1.Open(context.Background(), ConnInfo{Base: base, Modified: ours}); err != nil {
		return fmt.Errorf("RebaseDirect: open base→ours: %w", err)
	}
	w1, err := changeset.NewWriter(baseOurs)
	if err != nil {
		d1.Close()
		return fmt.Errorf("RebaseDirect: create base2ours writer: %w", err)
	}
	if err := d1.CreateChangeset(context.Background(), w1); err != nil {
		w1.Close()
		d1.Close()
		return fmt.Errorf("RebaseDirect: createChangeset base→ours: %w", err)
	}
	w1.Close()
	d1.Close()

	// If ours has no changes, skip the undo+theirs apply
	oursHasChanges := !fileBytesEmpty(baseOurs)

	if oursHasChanges {
		// 1b. Invert BASE→OURS to OURS→BASE
		if err := InvertChangeset(baseOurs, oursBase); err != nil {
			return fmt.Errorf("RebaseDirect: invert base→ours: %w", err)
		}

		// 1c. Apply OURS→BASE to OURS (undo local changes)
		rInv, err := changeset.NewReader(oursBase)
		if err != nil {
			return fmt.Errorf("RebaseDirect: open inverted changeset: %w", err)
		}
		dApply := NewSqliteDriver()
		if err := dApply.Open(context.Background(), ConnInfo{Base: ours}); err != nil {
			rInv.Close()
			return fmt.Errorf("RebaseDirect: open ours for undo: %w", err)
		}
		if err := dApply.ApplyChangeset(context.Background(), rInv); err != nil {
			rInv.Close()
			dApply.Close()
			return fmt.Errorf("RebaseDirect: apply undo changeset: %w", err)
		}
		rInv.Close()
		dApply.Close()
	}

	// --- Step 2: Apply theirs changes (BASE → THEIRS) ---

	// 2a. Create BASE→THEIRS changeset
	d2 := NewSqliteDriver()
	if err := d2.Open(context.Background(), ConnInfo{Base: base, Modified: theirs}); err != nil {
		return fmt.Errorf("RebaseDirect: open base→theirs: %w", err)
	}
	w2, err := changeset.NewWriter(baseTheirs)
	if err != nil {
		d2.Close()
		return fmt.Errorf("RebaseDirect: create base2theirs writer: %w", err)
	}
	if err := d2.CreateChangeset(context.Background(), w2); err != nil {
		w2.Close()
		d2.Close()
		return fmt.Errorf("RebaseDirect: createChangeset base→theirs: %w", err)
	}
	w2.Close()
	d2.Close()

	theirsHasChanges := !fileBytesEmpty(baseTheirs)

	if theirsHasChanges {
		// 2b. Apply BASE→THEIRS to OURS
		rTheirs, err := changeset.NewReader(baseTheirs)
		if err != nil {
			return fmt.Errorf("RebaseDirect: open theirs changeset: %w", err)
		}
		dApply2 := NewSqliteDriver()
		if err := dApply2.Open(context.Background(), ConnInfo{Base: ours}); err != nil {
			rTheirs.Close()
			return fmt.Errorf("RebaseDirect: open ours for theirs apply: %w", err)
		}
		if err := dApply2.ApplyChangeset(context.Background(), rTheirs); err != nil {
			rTheirs.Close()
			dApply2.Close()
			return fmt.Errorf("RebaseDirect: apply theirs changeset: %w", err)
		}
		rTheirs.Close()
		dApply2.Close()
	}

	// --- Step 3: Rebase local changes on top ---

	if oursHasChanges {
		// 3a. Create THEIRS→MERGED (rebased) changeset
		conflicts, err := Rebase(baseTheirs, baseOurs, theirsMerged)
		if err != nil {
			return fmt.Errorf("RebaseDirect: rebase: %w", err)
		}

		// 3b. Apply THEIRS→MERGED to OURS
		if !fileBytesEmpty(theirsMerged) {
			rMerged, err := changeset.NewReader(theirsMerged)
			if err != nil {
				return fmt.Errorf("RebaseDirect: open merged changeset: %w", err)
			}
			dApply3 := NewSqliteDriver()
			if err := dApply3.Open(context.Background(), ConnInfo{Base: ours}); err != nil {
				rMerged.Close()
				return fmt.Errorf("RebaseDirect: open ours for merged apply: %w", err)
			}
			if err := dApply3.ApplyChangeset(context.Background(), rMerged); err != nil {
				rMerged.Close()
				dApply3.Close()
				return fmt.Errorf("RebaseDirect: apply merged changeset: %w", err)
			}
			rMerged.Close()
			dApply3.Close()
		}

		// --- Step 4: Write conflicts ---
		if len(conflicts) > 0 {
			if err := writeConflictFile(conflictFile, conflicts); err != nil {
				return fmt.Errorf("RebaseDirect: write conflicts: %w", err)
			}
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// Conflict JSON serialization
// ---------------------------------------------------------------------------

// conflictItemJSON is the JSON-serializable form of a ConflictItem.
type conflictItemJSON struct {
	Column int `json:"column"`
	Base   any `json:"base,omitempty"`
	Old    any `json:"old,omitempty"`
	New    any `json:"new,omitempty"`
}

// conflictFeatureJSON is the JSON-serializable form of a ConflictFeature.
type conflictFeatureJSON struct {
	Table   string             `json:"table"`
	Type    string             `json:"type"`
	FID     string             `json:"fid"`
	Changes []conflictItemJSON `json:"changes"`
}

// conflictsJSON is the top-level JSON output.
type conflictsJSON struct {
	Geodiff []conflictFeatureJSON `json:"geodiff"`
}

// valueToJSON converts a changeset.Value to a JSON-compatible any value.
func valueToJSON(v changeset.Value) any {
	switch v.Type() {
	case changeset.TypeUndefined:
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
		// Base64-encode blob data
		b, _ := v.AsBlob()
		return base64.StdEncoding.EncodeToString(b)
	case changeset.TypeNull:
		return nil
	default:
		return nil
	}
}

// writeConflictFile writes conflicts to a JSON file.
func writeConflictFile(path string, conflicts []ConflictFeature) error {
	var entries []conflictFeatureJSON

	for _, cf := range conflicts {
		if !cf.IsValid() {
			continue
		}

		fj := conflictFeatureJSON{
			Table: cf.TableName,
			Type:  "conflict",
			FID:   fmt.Sprintf("%d", cf.PK),
		}

		for _, item := range cf.Items {
			cij := conflictItemJSON{
				Column: item.Column,
			}
			if v := valueToJSON(item.Base); v != nil {
				cij.Base = v
			}
			if v := valueToJSON(item.Theirs); v != nil {
				cij.Old = v
			}
			if v := valueToJSON(item.Ours); v != nil {
				cij.New = v
			}
			fj.Changes = append(fj.Changes, cij)
		}

		entries = append(entries, fj)
	}

	if len(entries) == 0 {
		return nil
	}

	output := conflictsJSON{Geodiff: entries}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("writeConflictFile: marshal: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
