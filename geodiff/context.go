/*
 GEODIFF - MIT License
 Copyright (C) 2020 Peter Petrik

 Go port of geodiffcontext.hpp + geodiffcontext.cpp — Context holds logger, error state, and table filters.
*/

package geodiff

import (
	"fmt"
	"sync"
)

// tablesFilterMode mirrors the C++ TablesFilterMode enum.
type tablesFilterMode int

const (
	filterNone tablesFilterMode = iota
	filterIncluded
	filterSkipped
)

// Context carries the logger, last error message, and table filtering rules for
// all geodiff operations. It is safe for concurrent use.
type Context struct {
	mu sync.Mutex

	logger      Logger
	maxLogLevel LogLevel

	filterMode      tablesFilterMode
	tablesToSkip    map[string]bool
	tablesToInclude map[string]bool

	lastError string
}

// NewContext creates a Context with a default logger at LevelError.
func NewContext() *Context {
	return &Context{
		logger:      NewDefaultLogger(LevelError),
		maxLogLevel: LevelError,
	}
}

// SetLogger replaces the current logger.
func (c *Context) SetLogger(logger Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logger = logger
}

// SetMaximumLoggerLevel sets the maximum log level.
func (c *Context) SetMaximumLoggerLevel(level LogLevel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maxLogLevel = level
}

// SetTablesToSkip sets a list of table names to exclude from all operations.
// Setting a non-empty list clears any previously set include list.
// An empty list resets table filtering entirely.
func (c *Context) SetTablesToSkip(tables []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.filterMode == filterIncluded {
		return fmt.Errorf("cannot set tables to skip when tables to include are already set")
	}

	if len(tables) == 0 {
		c.filterMode = filterNone
		c.tablesToSkip = nil
		c.tablesToInclude = nil
		return nil
	}

	c.filterMode = filterSkipped
	c.tablesToSkip = make(map[string]bool, len(tables))
	for _, t := range tables {
		c.tablesToSkip[t] = true
	}
	c.tablesToInclude = nil
	return nil
}

// SetTablesToInclude sets a list of table names to include in all operations.
// Setting a non-empty list clears any previously set skip list.
// An empty list resets table filtering entirely.
func (c *Context) SetTablesToInclude(tables []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.filterMode == filterSkipped {
		return fmt.Errorf("cannot set tables to include when tables to skip are already set")
	}

	if len(tables) == 0 {
		c.filterMode = filterNone
		c.tablesToSkip = nil
		c.tablesToInclude = nil
		return nil
	}

	c.filterMode = filterIncluded
	c.tablesToInclude = make(map[string]bool, len(tables))
	for _, t := range tables {
		c.tablesToInclude[t] = true
	}
	c.tablesToSkip = nil
	return nil
}

// IsTableSkipped returns true if the given table should be excluded from processing.
func (c *Context) IsTableSkipped(tableName string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.filterMode {
	case filterIncluded:
		return !c.tablesToInclude[tableName]
	case filterSkipped:
		return c.tablesToSkip[tableName]
	default:
		return false
	}
}

// setLastError records an error message.
func (c *Context) setLastError(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastError = msg
}

// LastError returns the last error message recorded on this context.
func (c *Context) LastError() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastError
}

// Logger returns the current logger.
func (c *Context) Logger() Logger {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.logger
}

// setAndLogError logs an error message and records it as the last error.
func (c *Context) setAndLogError(msg string) {
	c.mu.Lock()
	c.lastError = msg
	c.mu.Unlock()
	c.logger.Error(msg)
}
