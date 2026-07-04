/*
 GEODIFF - MIT License
 Copyright (C) 2019 Peter Petrik

 Go port of geodiffutils.hpp + geodiffutils.cpp — file helpers, exception types, and utilities.
*/

package geodiff

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
)

// ErrorCode mirrors the C++ GEODIFF return codes.
type ErrorCode int

const (
	Success   ErrorCode = 0
	Error     ErrorCode = 1
	Conflicts ErrorCode = 2
)

// GeoDiffError is the base error type wrapping a message and error code.
// It mirrors the C++ GeoDiffException and GeoDiffConflictsException classes.
type GeoDiffError struct {
	Code ErrorCode
	Msg  string
}

// Error implements the error interface.
func (e *GeoDiffError) Error() string {
	return e.Msg
}

// NewGeoDiffError creates a GeoDiffError with Code=Error.
func NewGeoDiffError(msg string) *GeoDiffError {
	return &GeoDiffError{Code: Error, Msg: msg}
}

// NewConflictError creates a GeoDiffError with Code=Conflicts.
func NewConflictError(msg string) *GeoDiffError {
	return &GeoDiffError{Code: Conflicts, Msg: msg}
}

// FileExists reports whether the file at path exists (never throws).
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FileCopy copies src to dst, overwriting dst if it exists.
// Returns nil on success.
func FileCopy(dst, src string) error {
	_ = os.Remove(dst)

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("filecopy: unable to open %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("filecopy: unable to create %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("filecopy: failed to copy %s to %s: %w", src, dst, err)
	}
	return nil
}

// FileRemove removes the file at path. Returns nil if the file does not exist.
func FileRemove(path string) error {
	if !FileExists(path) {
		return nil
	}
	return os.Remove(path)
}

// FlushString writes content to filename, overwriting if it exists.
// Returns nil on success.
func FlushString(filename, content string) error {
	return os.WriteFile(filename, []byte(content), 0644)
}

// TmpDir returns the system temporary directory (with trailing separator).
// Uses $TMPDIR on Unix, falling back to /tmp/.
func TmpDir() string {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		dir = "/tmp/"
	}
	// Ensure trailing separator.
	if len(dir) > 0 && dir[len(dir)-1] != '/' && dir[len(dir)-1] != '\\' {
		dir += "/"
	}
	return dir
}

// RandomString returns a string of alphanumeric characters of the given length.
func RandomString(length int) string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// RandomTmpFilename returns a randomly-generated filename in the temp directory
// (e.g. "/tmp/geodiff_a3Bx9z").
func RandomTmpFilename() string {
	return filepath.Join(TmpDir(), "geodiff_"+RandomString(6))
}

// isLayerTable returns true if the table name is a user-layer table (not GPKG metadata).
func isLayerTable(tableName string) bool {
	if len(tableName) >= 5 && tableName[:5] == "gpkg_" {
		return false
	}
	if len(tableName) >= 6 && tableName[:6] == "rtree_" {
		return false
	}
	if tableName == "sqlite_sequence" {
		return false
	}
	return true
}

// filterTableNames filters a list of table names by isLayerTable and the context's
// skip/include rules. Returns the filtered list.
func filterTableNames(ctx *Context, names []string) []string {
	var out []string
	for _, n := range names {
		if !isLayerTable(n) {
			continue
		}
		if ctx.IsTableSkipped(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}
