// Package logging provides a structured, level-aware logger with automatic
// file rotation and Docker container detection. It follows the commonLogging
// policy for level discipline:
//
//   - TRACE/DEBUG are diagnostic tools, never active in production permanently.
//   - INFO is the default production level.
//   - WARN indicates recoverable anomalies (retries, fallbacks).
//   - ERROR indicates failures requiring attention.
//   - FATAL indicates unrecoverable errors; the process exits after logging.
//
// Environment variables:
//
//	LOG_LEVEL        — minimum severity (default: info)
//	LOG_DIR          — root directory for log files (default: <binary>/logs)
//	LOG_MAX_SIZE     — max file size in bytes for size-based rotation (default: 10MiB)
//	LOG_STDOUT_ONLY  — force stdout-only mode (auto-detected in Docker)
//
// Log line format:
//
//	[TIMESTAMP][LEVEL] message
//	[TIMESTAMP][LEVEL][file.go:line <- caller.go:line] message   (with stack)
//	[BACK][TIMESTAMP][LEVEL] message                             (stdout-only/Docker)
package logging

import "strings"

// Level represents the severity of a log entry. Levels are ordered from
// least severe (LevelTrace) to most severe (LevelFatal). Messages below
// the logger's configured level are silently discarded.
type Level int

const (
	// LevelTrace is the lowest granularity: raw payloads, loop iterations,
	// variable dumps. Generates very high volume. Enable only for targeted,
	// short-lived investigations. Never active in production permanently.
	LevelTrace Level = iota

	// LevelDebug captures control-flow decisions, variable snapshots,
	// cache hits/misses, SQL queries. Safe for staging; in production
	// only during incident response.
	LevelDebug

	// LevelInfo is the default production level. Each INFO line should be
	// meaningful to an operator without prior context: startup, shutdown,
	// connections established, configuration loaded.
	LevelInfo

	// LevelWarn indicates recoverable anomalies: retries to external
	// services, fallback to defaults, approaching capacity limits,
	// deprecation warnings.
	LevelWarn

	// LevelError indicates failures that need attention: failed requests
	// returning 5xx, broken database connections, exhausted retries.
	// Each ERROR should be potentially actionable.
	LevelError

	// LevelFatal indicates unrecoverable errors. The process terminates
	// immediately after logging. Use with extreme moderation: missing
	// critical config, failed migrations, port already in use.
	LevelFatal
)

// levelNames maps each Level to its human-readable string representation.
var levelNames = map[Level]string{
	LevelTrace: "TRACE",
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
	LevelFatal: "FATAL",
}

// ParseLevel converts a case-insensitive string into a Level.
// Accepts "WARNING" as an alias for LevelWarn.
// Returns LevelInfo for any unrecognised input (safe default for production).
func ParseLevel(s string) Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRACE":
		return LevelTrace
	case "DEBUG":
		return LevelDebug
	case "INFO":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "FATAL":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// String returns the human-readable name for the level (e.g. "INFO").
// Returns "UNKNOWN" for undefined levels.
func (l Level) String() string {
	if name, ok := levelNames[l]; ok {
		return name
	}
	return "UNKNOWN"
}
