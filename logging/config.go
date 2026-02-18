package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Environment variable names used by ConfigFromEnv.
const (
	// EnvLogLevel controls the backend log level.
	// Valid values: trace, debug, info, warn, error, fatal.
	EnvLogLevel = "LOG_LEVEL"

	// EnvLogDir overrides the base directory for log files.
	// Default: ./logs relative to the binary.
	EnvLogDir = "LOG_DIR"

	// EnvLogMaxSize overrides the max size in bytes for size-based rotation.
	// Default: 10485760 (10 MiB).
	EnvLogMaxSize = "LOG_MAX_SIZE"

	// EnvLogStdoutOnly forces all output to stdout (no file writing).
	// Accepts "true", "1", "yes" to enable; "false", "0", "no" to disable.
	// Auto-detected when running inside Docker (/.dockerenv) or Podman
	// (/run/.containerenv).
	EnvLogStdoutOnly = "LOG_STDOUT_ONLY"
)

// RotationPolicy determines when log files are rotated.
type RotationPolicy int

const (
	// RotationNone creates a single file with no rotation.
	RotationNone RotationPolicy = iota

	// RotationDaily creates a new log directory (YYYY/MM/DD/) each day
	// at midnight. This is the default policy.
	RotationDaily

	// RotationSize rotates when the current file reaches MaxSizeBytes.
	// The old file is renamed with an HHMMSS suffix.
	RotationSize
)

// Config holds all tunables for a Logger instance. Use ConfigFromEnv to
// build a Config from environment variables, or construct one manually
// for full control.
type Config struct {
	// Level is the minimum severity that will be written.
	Level Level

	// EnableStack appends a short call-stack trace (file:line <- file:line)
	// to every log line. Useful for tracing error origins.
	EnableStack bool

	// EnableFile activates writing to disk (logs/YYYY/MM/DD/<filename>).
	// Ignored when StdoutOnly is true.
	EnableFile bool

	// StdoutOnly disables file writing and sends everything to stdout.
	// When true, each line is prefixed with StreamTag (e.g. "[BACK]").
	// Automatically enabled inside Docker/Podman containers.
	StdoutOnly bool

	// StreamTag is the prefix added to stdout lines in StdoutOnly mode.
	// Default: "BACK". The prefix appears as [BACK] in the output.
	StreamTag string

	// LogDir is the root directory for log files.
	// Subdirectories YYYY/MM/DD/ are created automatically.
	LogDir string

	// RotationPolicy controls when a new log file is started.
	RotationPolicy RotationPolicy

	// MaxSizeBytes is the file size threshold for RotationSize policy.
	// Default: 10 MiB. Ignored for other rotation policies.
	MaxSizeBytes int64

	// StackDepth limits how many call frames getStackTrace returns.
	// Default: 10.
	StackDepth int

	// Filename is the log file name inside the date directory.
	// Default: "back.log".
	Filename string
}

// ConfigFromEnv builds a Config by reading environment variables and applying
// sensible defaults. The caller can override individual fields afterwards.
//
// Defaults:
//   - Level: INFO
//   - EnableStack: true
//   - EnableFile: true (unless inside Docker)
//   - RotationPolicy: RotationDaily
//   - MaxSizeBytes: 10 MiB
//   - Filename: "back.log"
func ConfigFromEnv() Config {
	stdoutOnly := isStdoutOnly()

	cfg := Config{
		Level:          LevelInfo,
		EnableStack:    true,
		EnableFile:     !stdoutOnly,
		StdoutOnly:     stdoutOnly,
		StreamTag:      "BACK",
		LogDir:         defaultLogDir(),
		RotationPolicy: RotationDaily,
		MaxSizeBytes:   10 * 1024 * 1024,
		StackDepth:     10,
		Filename:       "back.log",
	}

	if v := os.Getenv(EnvLogLevel); v != "" {
		cfg.Level = ParseLevel(v)
	}
	if v := os.Getenv(EnvLogDir); v != "" {
		cfg.LogDir = v
	}
	if v := os.Getenv(EnvLogMaxSize); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			cfg.MaxSizeBytes = n
		}
	}

	return cfg
}

// isStdoutOnly returns true when LOG_STDOUT_ONLY is explicitly set or when
// running inside a Docker or Podman container (detected via sentinel files).
func isStdoutOnly() bool {
	if v := os.Getenv(EnvLogStdoutOnly); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}
	return false
}

// defaultLogDir returns a "logs" directory next to the running binary.
// Falls back to a relative "logs" path if the binary location cannot
// be determined.
func defaultLogDir() string {
	exe, err := os.Executable()
	if err == nil {
		return filepath.Join(filepath.Dir(exe), "logs")
	}
	return "logs"
}

// DatePath returns the YYYY/MM/DD subdirectory for a given time, used
// for organizing log files by date.
func DatePath(t time.Time) string {
	return fmt.Sprintf("%04d/%02d/%02d", t.Year(), t.Month(), t.Day())
}
