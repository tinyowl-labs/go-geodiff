/*
 GEODIFF - MIT License
 Copyright (C) 2020 Martin Dobias

 Ported to Go: changesetwriter.h + changesetwriter.cpp
*/

package changeset

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/tinyowl-labs/go-geodiff/varint"
)

// Writer serializes ChangesetEntry values into the binary changeset format.
//
// Usage:
//
//	w, _ := NewWriter("my.changeset")
//	w.BeginTable(table)
//	for _, entry := range entries {
//	    w.WriteEntry(entry)
//	}
//	w.Close()
type Writer struct {
	file *os.File

	mCurrentTable ChangesetTable // currently processed table
	tmp           [varint.MaxVarintLen]byte
}

// NewWriter creates a new changeset file (overwrites if it exists).
func NewWriter(filename string) (*Writer, error) {
	f, err := os.Create(filename)
	if err != nil {
		return nil, fmt.Errorf("changeset writer: %w", err)
	}
	return &Writer{file: f}, nil
}

// BeginTable writes the table header. All subsequent calls to WriteEntry
// are associated with this table until the next call to BeginTable.
func (w *Writer) BeginTable(table ChangesetTable) error {
	w.mCurrentTable = table

	if err := w.writeByte('T'); err != nil {
		return err
	}
	if err := w.writeVarint(table.ColumnCount()); err != nil {
		return err
	}
	for _, pk := range table.PrimaryKeys {
		b := byte(0)
		if pk {
			b = 1
		}
		if err := w.writeByte(b); err != nil {
			return err
		}
	}
	return w.writeNullTerminatedString(table.Name)
}

// WriteEntry writes a single changeset entry for the current table.
func (w *Writer) WriteEntry(entry ChangesetEntry) error {
	if entry.Op != OpInsert && entry.Op != OpUpdate && entry.Op != OpDelete {
		return fmt.Errorf("writer error: wrong op for changeset entry")
	}

	if err := w.writeByte(byte(entry.Op)); err != nil {
		return err
	}
	if err := w.writeByte(0); err != nil {
		return err
	} // "indirect" always false

	if entry.Op != OpInsert {
		if err := w.writeRowValues(entry.OldValues); err != nil {
			return err
		}
	}
	if entry.Op != OpDelete {
		if err := w.writeRowValues(entry.NewValues); err != nil {
			return err
		}
	}
	return nil
}

// Close flushes and closes the underlying file.
func (w *Writer) Close() error {
	return w.file.Close()
}

// --- private write helpers ---

func (w *Writer) writeByte(c byte) error {
	_, err := w.file.Write([]byte{c})
	return err
}

func (w *Writer) writeVarint(n int) error {
	nBytes := varint.PutVarint(w.tmp[:], uint32(n))
	_, err := w.file.Write(w.tmp[:nBytes])
	return err
}

func (w *Writer) writeNullTerminatedString(s string) error {
	if _, err := w.file.Write([]byte(s)); err != nil {
		return err
	}
	_, err := w.file.Write([]byte{0})
	return err
}

func (w *Writer) writeRowValues(values []Value) error {
	if len(values) != w.mCurrentTable.ColumnCount() {
		return fmt.Errorf("writer error: wrong number of rows in the entry")
	}

	for i := 0; i < w.mCurrentTable.ColumnCount(); i++ {
		typ := values[i].Type()
		if err := w.writeByte(byte(typ)); err != nil {
			return err
		}

		switch typ {
		case TypeInt: // 0x01
			binary.BigEndian.PutUint64(w.tmp[:8], uint64(values[i].AsInt()))
			if _, err := w.file.Write(w.tmp[:8]); err != nil {
				return err
			}

		case TypeDouble: // 0x02
			binary.BigEndian.PutUint64(w.tmp[:8], math.Float64bits(values[i].AsDouble()))
			if _, err := w.file.Write(w.tmp[:8]); err != nil {
				return err
			}

		case TypeText: // 0x03
			s := values[i].AsText()
			if err := w.writeVarint(len(s)); err != nil {
				return err
			}
			if _, err := w.file.Write([]byte(s)); err != nil {
				return err
			}

		case TypeBlob: // 0x04
			b := values[i].AsBlob()
			if err := w.writeVarint(len(b)); err != nil {
				return err
			}
			if _, err := w.file.Write(b); err != nil {
				return err
			}

		case TypeNull: // 0x05
			// nothing extra to write

		case TypeUndefined: // 0x00
			// nothing extra to write

		default:
			return fmt.Errorf("writer error: unexpected entry type %d", typ)
		}
	}
	return nil
}
