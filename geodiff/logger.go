/*
 GEODIFF - MIT License
 Copyright (C) 2020 Peter Petrik

 Go port of geodifflogger.hpp + geodifflogger.cpp — Logger interface and default implementation.
*/

package geodiff

import (
	"fmt"
	"os"
)

// LogLevel mirrors the C++ GEODIFF_LoggerLevel enum.
type LogLevel int

const (
	LevelError   LogLevel = 1
	LevelWarning LogLevel = 2
	LevelInfo    LogLevel = 3
	LevelDebug   LogLevel = 4
)

// Logger is the interface for geodiff logging.
type Logger interface {
	Error(msg string)
	Warning(msg string)
	Info(msg string)
	Debug(msg string)
}

// DefaultLogger writes to stdout (info/debug) and stderr (warning/error).
type DefaultLogger struct {
	level LogLevel
}

// NewDefaultLogger creates a DefaultLogger that only emits messages at or
// below the given level (lower numeric value = higher severity).
func NewDefaultLogger(level LogLevel) *DefaultLogger {
	return &DefaultLogger{level: level}
}

func (l *DefaultLogger) Error(msg string) {
	if l.level >= LevelError {
		fmt.Fprintln(os.Stderr, "ERROR:", msg)
	}
}

func (l *DefaultLogger) Warning(msg string) {
	if l.level >= LevelWarning {
		fmt.Fprintln(os.Stderr, "WARNING:", msg)
	}
}

func (l *DefaultLogger) Info(msg string) {
	if l.level >= LevelInfo {
		fmt.Fprintln(os.Stdout, "INFO:", msg)
	}
}

func (l *DefaultLogger) Debug(msg string) {
	if l.level >= LevelDebug {
		fmt.Fprintln(os.Stdout, "DEBUG:", msg)
	}
}
