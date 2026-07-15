// Package changeset implements the geodiff changeset data model and binary format.
//
// This is a port of changeset.h, changesetreader.cpp, changesetwriter.cpp,
// and changesetutils.cpp from the geodiff C++ library (MIT, Lutra Consulting).
package changeset

import (
	"fmt"
)

// Value represents a single column value in a changeset entry.
// It mirrors the C++ Value struct with the same type system.
//
// Possible types mirror SQLite's storage classes plus an Undefined type
// used in UPDATE entries to indicate an unmodified column.
type Value struct {
	mType    ValueType
	intVal   int64
	floatVal float64
	strVal   string
	blobVal  []byte
}

// ValueType indicates the type of value stored.
type ValueType byte

const (
	TypeUndefined ValueType = 0 // Value has not changed (used in UPDATE entries)
	TypeInt       ValueType = 1 // 64-bit signed integer
	TypeDouble    ValueType = 2 // 64-bit IEEE 754 float
	TypeText      ValueType = 3 // UTF-8 text
	TypeBlob      ValueType = 4 // Binary data
	TypeNull      ValueType = 5 // SQL NULL
)

// OperationType is the type of operation in a changeset entry.
type OperationType byte

const (
	OpInsert OperationType = 18 // 0x12, equal to SQLITE_INSERT
	OpUpdate OperationType = 23 // 0x17, equal to SQLITE_UPDATE
	OpDelete OperationType = 9  // 0x09, equal to SQLITE_DELETE
)

// --- Value constructors and accessors ---

// NewValueInt creates a Value holding an int64.
func NewValueInt(n int64) Value {
	return Value{mType: TypeInt, intVal: n}
}

// NewValueDouble creates a Value holding a float64.
func NewValueDouble(n float64) Value {
	return Value{mType: TypeDouble, floatVal: n}
}

// NewValueText creates a Value holding a text string.
func NewValueText(s string) Value {
	return Value{mType: TypeText, strVal: s}
}

// NewValueBlob creates a Value holding binary data.
func NewValueBlob(b []byte) Value {
	return Value{mType: TypeBlob, blobVal: b}
}

// NewValueNull creates a Value representing SQL NULL.
func NewValueNull() Value {
	return Value{mType: TypeNull}
}

// NewValueUndefined creates an undefined Value (column not modified).
func NewValueUndefined() Value {
	return Value{mType: TypeUndefined}
}

// Type returns the ValueType of this value.
func (v Value) Type() ValueType { return v.mType }

// IsUndefined returns true if the value type is Undefined.
func (v Value) IsUndefined() bool { return v.mType == TypeUndefined }

// IsNull returns true if the value type is Null.
func (v Value) IsNull() bool { return v.mType == TypeNull }

// AsInt returns the integer value. Returns an error if type is not TypeInt.
func (v Value) AsInt() (int64, error) {
	if v.mType != TypeInt {
		return 0, fmt.Errorf("Value.AsInt called on %s", v.mType)
	}
	return v.intVal, nil
}

// AsDouble returns the double value. Returns an error if type is not TypeDouble.
func (v Value) AsDouble() (float64, error) {
	if v.mType != TypeDouble {
		return 0, fmt.Errorf("Value.AsDouble called on %s", v.mType)
	}
	return v.floatVal, nil
}

// AsText returns the text value. Returns an error if type is not TypeText or TypeBlob.
func (v Value) AsText() (string, error) {
	if v.mType != TypeText && v.mType != TypeBlob {
		return "", fmt.Errorf("Value.AsText called on %s", v.mType)
	}
	return v.strVal, nil
}

// AsBlob returns the blob value. Returns an error if type is not TypeText or TypeBlob.
func (v Value) AsBlob() ([]byte, error) {
	if v.mType != TypeText && v.mType != TypeBlob {
		return nil, fmt.Errorf("Value.AsBlob called on %s", v.mType)
	}
	return v.blobVal, nil
}

// String returns a human-readable representation of the value.
func (v Value) String() string {
	switch v.mType {
	case TypeUndefined:
		return "undefined"
	case TypeInt:
		return fmt.Sprintf("int(%d)", v.intVal)
	case TypeDouble:
		return fmt.Sprintf("double(%g)", v.floatVal)
	case TypeText:
		return fmt.Sprintf("text(%q)", v.strVal)
	case TypeBlob:
		return fmt.Sprintf("blob(%d bytes)", len(v.blobVal))
	case TypeNull:
		return "null"
	default:
		return "unknown"
	}
}

// Equal reports whether two Values are equal.
func (v Value) Equal(other Value) bool {
	if v.mType != other.mType {
		return false
	}
	switch v.mType {
	case TypeUndefined, TypeNull:
		return true
	case TypeInt:
		return v.intVal == other.intVal
	case TypeDouble:
		return v.floatVal == other.floatVal
	case TypeText:
		return v.strVal == other.strVal
	case TypeBlob:
		if len(v.blobVal) != len(other.blobVal) {
			return false
		}
		for i := range v.blobVal {
			if v.blobVal[i] != other.blobVal[i] {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// String returns a debug representation of the ValueType.
func (t ValueType) String() string {
	switch t {
	case TypeUndefined:
		return "undefined"
	case TypeInt:
		return "int"
	case TypeDouble:
		return "double"
	case TypeText:
		return "text"
	case TypeBlob:
		return "blob"
	case TypeNull:
		return "null"
	default:
		return fmt.Sprintf("unknown(%d)", t)
	}
}

// String returns a human-readable representation of the operation.
func (op OperationType) String() string {
	switch op {
	case OpInsert:
		return "insert"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	default:
		return fmt.Sprintf("unknown(%d)", op)
	}
}

// --- ChangesetTable ---

// ChangesetTable holds table metadata from the changeset header.
// It maps directly to the C++ ChangesetTable struct.
type ChangesetTable struct {
	Name        string // Unqualified table name
	PrimaryKeys []bool // true for each PK column, false otherwise
}

// ColumnCount returns the number of columns in this table.
func (t *ChangesetTable) ColumnCount() int {
	return len(t.PrimaryKeys)
}

// Clone returns a deep copy of the table.
func (t *ChangesetTable) Clone() *ChangesetTable {
	pk := make([]bool, len(t.PrimaryKeys))
	copy(pk, t.PrimaryKeys)
	return &ChangesetTable{Name: t.Name, PrimaryKeys: pk}
}

// --- ChangesetEntry ---

// ChangesetEntry represents a single INSERT, UPDATE, or DELETE operation
// within a changeset.
//
// Value semantics per operation:
//   - INSERT: newValues populated, oldValues unused
//   - DELETE: oldValues populated, newValues unused
//   - UPDATE: both populated; undefined values indicate unchanged columns
//     PK columns in oldValues are always present.
type ChangesetEntry struct {
	Op        OperationType
	OldValues []Value
	NewValues []Value
	Table     ChangesetTable
}

// Clone returns a deep copy of the entry.
func (e *ChangesetEntry) Clone() *ChangesetEntry {
	ce := &ChangesetEntry{
		Op:    e.Op,
		Table: e.Table,
	}
	if len(e.OldValues) > 0 {
		ce.OldValues = make([]Value, len(e.OldValues))
		copy(ce.OldValues, e.OldValues)
	}
	if len(e.NewValues) > 0 {
		ce.NewValues = make([]Value, len(e.NewValues))
		copy(ce.NewValues, e.NewValues)
	}
	return ce
}
