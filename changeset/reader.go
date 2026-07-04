/*
 GEODIFF - MIT License
 Copyright (C) 2020 Martin Dobias

 Ported to Go: changesetreader.h + changesetreader.cpp
*/

package changeset

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/tinyowl-labs/go-geodiff/varint"
)

// Reader parses the binary changeset format from a file.
//
// Usage:
//
//	r, err := NewReader("my.changeset")
//	for {
//	    entry, err := r.NextEntry()
//	    if entry == nil { break }
//	    // process entry
//	}
//	r.Close()
type Reader struct {
	buf    []byte
	offset int

	mCurrentTable ChangesetTable // currently processed table
}

// NewReader opens a changeset file and loads its contents into memory.
func NewReader(filename string) (*Reader, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("changeset reader: %w", err)
	}
	return &Reader{buf: data}, nil
}

// NextEntry reads the next ChangesetEntry from the buffer.
// Returns nil, nil when the changeset has been fully consumed (EOF).
// Returns nil, error on malformed input.
func (r *Reader) NextEntry() (*ChangesetEntry, error) {
	for {
		if r.offset >= len(r.buf) {
			return nil, nil // EOF
		}

		typ, err := r.readByte()
		if err != nil {
			return nil, err
		}

		if typ == 'T' {
			if err := r.readTableRecord(); err != nil {
				return nil, err
			}
			// continue reading, we want an entry
		} else if typ == byte(OpInsert) || typ == byte(OpUpdate) || typ == byte(OpDelete) {
			// indirect-change flag byte (always 0, ignored)
			if _, err := r.readByte(); err != nil {
				return nil, err
			}

			entry := &ChangesetEntry{
				Op:    OperationType(typ),
				Table: &r.mCurrentTable,
			}

			if typ != byte(OpInsert) {
				if err := r.readRowValues(&entry.OldValues); err != nil {
					return nil, err
				}
			}
			if typ != byte(OpDelete) {
				if err := r.readRowValues(&entry.NewValues); err != nil {
					return nil, err
				}
			}

			return entry, nil
		} else {
			return nil, r.readerError(fmt.Sprintf("unknown entry type %d", typ))
		}
	}
}

// IsEmpty returns true if the changeset contains no data.
func (r *Reader) IsEmpty() bool {
	return len(r.buf) == 0
}

// Rewind resets the reader position back to the start of the changeset
// and clears the current table state.
func (r *Reader) Rewind() {
	r.offset = 0
	r.mCurrentTable = ChangesetTable{}
}

// Close releases resources. After Close, the reader cannot be reused.
func (r *Reader) Close() error {
	r.buf = nil
	return nil
}

// --- private read helpers ---

func (r *Reader) readByte() (byte, error) {
	if r.offset >= len(r.buf) {
		return 0, r.readerError("readByte: at the end of buffer")
	}
	b := r.buf[r.offset]
	r.offset++
	return b, nil
}

func (r *Reader) readVarint() (int, error) {
	v, n, err := varint.GetVarint(r.buf, r.offset)
	if err != nil {
		return 0, r.readerError(err.Error())
	}
	r.offset += n
	return int(v), nil
}

func (r *Reader) readNullTerminatedString() (string, error) {
	start := r.offset
	for r.offset < len(r.buf) && r.buf[r.offset] != 0 {
		r.offset++
	}
	if r.offset >= len(r.buf) {
		return "", r.readerError("readNullTerminatedString: at the end of buffer")
	}
	s := string(r.buf[start:r.offset])
	r.offset++ // skip the null terminator
	return s, nil
}

func (r *Reader) readRowValues(values *[]Value) error {
	nCol := r.mCurrentTable.ColumnCount()
	if len(*values) != nCol {
		*values = make([]Value, nCol)
	}

	for i := 0; i < nCol; i++ {
		typ, err := r.readByte()
		if err != nil {
			return err
		}

		switch ValueType(typ) {
		case TypeInt: // 0x01
			if r.offset+8 > len(r.buf) {
				return r.readerError("readRowValues: int: at the end of buffer")
			}
			x := binary.BigEndian.Uint64(r.buf[r.offset:])
			r.offset += 8
			(*values)[i] = NewValueInt(int64(x))

		case TypeDouble: // 0x02
			if r.offset+8 > len(r.buf) {
				return r.readerError("readRowValues: double: at the end of buffer")
			}
			bits := binary.BigEndian.Uint64(r.buf[r.offset:])
			r.offset += 8
			(*values)[i] = NewValueDouble(math.Float64frombits(bits))

		case TypeText: // 0x03
			length, err := r.readVarint()
			if err != nil {
				return err
			}
			if r.offset+length > len(r.buf) {
				return r.readerError("readRowValues: text: at the end of buffer")
			}
			(*values)[i] = NewValueText(string(r.buf[r.offset : r.offset+length]))
			r.offset += length

		case TypeBlob: // 0x04
			length, err := r.readVarint()
			if err != nil {
				return err
			}
			if r.offset+length > len(r.buf) {
				return r.readerError("readRowValues: blob: at the end of buffer")
			}
			data := make([]byte, length)
			copy(data, r.buf[r.offset:r.offset+length])
			(*values)[i] = NewValueBlob(data)
			r.offset += length

		case TypeNull: // 0x05
			(*values)[i] = NewValueNull()

		case TypeUndefined: // 0x00
			(*values)[i] = NewValueUndefined()

		default:
			return r.readerError(fmt.Sprintf("readRowValues: unexpected entry type %d", typ))
		}
	}
	return nil
}

func (r *Reader) readTableRecord() error {
	// A 'table' record consists of:
	//   * A constant 'T' character (already consumed),
	//   * Number of columns in said table (a varint),
	//   * An array of nCol bytes (PK flags),
	//   * A nul-terminated table name.

	nCol, err := r.readVarint()
	if err != nil {
		return err
	}
	if nCol < 0 || nCol > 65536 {
		return r.readerError("readTableRecord: unexpected number of columns")
	}

	r.mCurrentTable.PrimaryKeys = make([]bool, nCol)
	for i := 0; i < nCol; i++ {
		b, err := r.readByte()
		if err != nil {
			return err
		}
		r.mCurrentTable.PrimaryKeys[i] = b != 0
	}

	name, err := r.readNullTerminatedString()
	if err != nil {
		return err
	}
	r.mCurrentTable.Name = name
	return nil
}

func (r *Reader) readerError(message string) error {
	return fmt.Errorf("reader error at offset %d: %s", r.offset, message)
}
