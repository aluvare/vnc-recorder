package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Logger is a concurrency-safe, level-aware logger that writes to stdout
// and optionally to dated log files (logs/YYYY/MM/DD/<filename>).
//
// All public methods are safe for concurrent use from multiple goroutines.
//
// Log line format:
//
//	Without stack: [2006-01-02 15:04:05.000][INFO] message
//	With stack:    [2006-01-02 15:04:05.000][INFO][main.go:42 <- server.go:10] message
//	Docker mode:   [BACK][2006-01-02 15:04:05.000][INFO] message
type Logger struct {
	mu             sync.Mutex
	level          Level
	enableStack    bool
	enableFile     bool
	stdoutOnly     bool
	streamTag      string
	logDir         string
	filename       string
	rotationPolicy RotationPolicy
	maxSizeBytes   int64
	stackDepth     int
	currentFile    *os.File
	currentDate    string
	currentSize    int64
	writers        []io.Writer
}

// New creates a Logger from the supplied Config. It applies sensible defaults
// for any zero-value fields:
//   - StackDepth defaults to 10
//   - MaxSizeBytes defaults to 10 MiB
//   - LogDir defaults to <binary>/logs
//   - Filename defaults to "back.log"
//
// If EnableFile is true, the log file is opened immediately.
func New(cfg Config) *Logger {
	if cfg.StackDepth <= 0 {
		cfg.StackDepth = 10
	}
	if cfg.MaxSizeBytes <= 0 {
		cfg.MaxSizeBytes = 10 * 1024 * 1024
	}
	if cfg.LogDir == "" {
		cfg.LogDir = defaultLogDir()
	}
	if cfg.Filename == "" {
		cfg.Filename = "back.log"
	}

	l := &Logger{
		level:          cfg.Level,
		enableStack:    cfg.EnableStack,
		enableFile:     cfg.EnableFile && !cfg.StdoutOnly,
		stdoutOnly:     cfg.StdoutOnly,
		streamTag:      cfg.StreamTag,
		logDir:         cfg.LogDir,
		filename:       cfg.Filename,
		rotationPolicy: cfg.RotationPolicy,
		maxSizeBytes:   cfg.MaxSizeBytes,
		stackDepth:     cfg.StackDepth,
		writers:        []io.Writer{os.Stdout},
	}

	if l.enableFile {
		_ = l.rotateIfNeeded()
	}

	return l
}

// Default returns a Logger configured from environment variables with
// sensible defaults. Equivalent to New(ConfigFromEnv()).
// This is the recommended way to create a logger in most applications.
func Default() *Logger {
	return New(ConfigFromEnv())
}

// SetLevel changes the minimum severity at runtime. Messages below this
// level are silently discarded. Thread-safe.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// GetLevel returns the current minimum severity level. Thread-safe.
func (l *Logger) GetLevel() Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

// LogDir returns the root log directory used by this logger.
func (l *Logger) LogDir() string {
	return l.logDir
}

// getLogFilePath returns the full path for a log file at the given time,
// incorporating the date-based directory structure.
func (l *Logger) getLogFilePath(t time.Time) string {
	return filepath.Join(l.logDir, DatePath(t), l.filename)
}

// rotateIfNeeded checks whether a new log file should be opened based on
// the rotation policy. Must be called with l.mu held.
func (l *Logger) rotateIfNeeded() error {
	now := time.Now()
	dateStr := now.Format("2006-01-02")

	needRotate := false

	switch l.rotationPolicy {
	case RotationDaily:
		if l.currentDate != dateStr {
			needRotate = true
		}
	case RotationSize:
		if l.currentSize >= l.maxSizeBytes {
			needRotate = true
		}
	case RotationNone:
		if l.currentFile == nil {
			needRotate = true
		}
	}

	if l.currentFile == nil {
		needRotate = true
	}

	if !needRotate {
		return nil
	}

	// Close existing file before opening a new one.
	if l.currentFile != nil {
		l.currentFile.Close()
		l.currentFile = nil
	}

	logPath := l.getLogFilePath(now)
	logDir := filepath.Dir(logPath)

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("logging: mkdir %s: %w", logDir, err)
	}

	// For size-based rotation, rename the existing file with a time suffix
	// before creating a new one.
	if l.rotationPolicy == RotationSize && l.currentSize >= l.maxSizeBytes {
		if _, err := os.Stat(logPath); err == nil {
			rotatedPath := logPath + "." + now.Format("150405")
			_ = os.Rename(logPath, rotatedPath)
		}
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("logging: open %s: %w", logPath, err)
	}

	l.currentFile = f
	l.currentDate = dateStr

	if info, err := f.Stat(); err == nil {
		l.currentSize = info.Size()
	} else {
		l.currentSize = 0
	}

	l.writers = []io.Writer{os.Stdout, f}
	return nil
}

// getStackTrace walks the call stack (skipping logging internals) and
// returns a compact trace like "main.go:42 <- server.go:10 <- router.go:55".
// The number of frames is limited by l.stackDepth.
func (l *Logger) getStackTrace() string {
	var pcs [32]uintptr
	// Skip 4 frames: runtime.Callers, getStackTrace, log, and the public method.
	n := runtime.Callers(4, pcs[:])
	if n == 0 {
		return ""
	}

	frames := runtime.CallersFrames(pcs[:n])
	var sb strings.Builder
	count := 0

	for {
		frame, more := frames.Next()
		if count >= l.stackDepth {
			break
		}
		// Skip Go runtime frames (not useful for application debugging).
		if strings.Contains(frame.File, "runtime/") {
			if !more {
				break
			}
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString(" <- ")
		}
		sb.WriteString(fmt.Sprintf("%s:%d", filepath.Base(frame.File), frame.Line))
		count++
		if !more {
			break
		}
	}

	return sb.String()
}

// log is the internal method that formats and writes a log line. It handles
// level filtering, file rotation, stack traces, and Docker stream tags.
// Must not be called directly; use the public Trace/Debug/Info/Warn/Error/Fatal methods.
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.enableFile {
		_ = l.rotateIfNeeded()
	}

	now := time.Now()
	timestamp := now.Format("2006-01-02 15:04:05.000")
	levelName := level.String()

	var msg string
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	} else {
		msg = format
	}

	var logLine string
	if l.enableStack {
		stack := l.getStackTrace()
		logLine = fmt.Sprintf("[%s][%s][%s] %s\n", timestamp, levelName, stack, msg)
	} else {
		logLine = fmt.Sprintf("[%s][%s] %s\n", timestamp, levelName, msg)
	}

	// In Docker/stdout-only mode, prefix with stream tag so log aggregators
	// can distinguish backend from frontend streams.
	if l.stdoutOnly && l.streamTag != "" {
		logLine = fmt.Sprintf("[%s]%s", l.streamTag, logLine)
	}

	for _, w := range l.writers {
		_, _ = io.WriteString(w, logLine)
	}

	if l.currentFile != nil {
		l.currentSize += int64(len(logLine))
	}
}

// Trace logs at TRACE level. Use only for targeted, short-lived diagnostics:
// raw payloads, loop iteration state, variable dumps.
func (l *Logger) Trace(args ...interface{}) { l.log(LevelTrace, "%s", fmt.Sprint(args...)) }

// Tracef logs at TRACE level with printf-style formatting.
func (l *Logger) Tracef(format string, args ...interface{}) { l.log(LevelTrace, format, args...) }

// Debug logs at DEBUG level. For control-flow decisions, cache hits/misses,
// variable snapshots. Safe in staging; production only under incident response.
func (l *Logger) Debug(args ...interface{}) { l.log(LevelDebug, "%s", fmt.Sprint(args...)) }

// Debugf logs at DEBUG level with printf-style formatting.
func (l *Logger) Debugf(format string, args ...interface{}) { l.log(LevelDebug, format, args...) }

// Info logs at INFO level. Default production level: startup/shutdown,
// connections established, requests served, configuration loaded.
func (l *Logger) Info(args ...interface{}) { l.log(LevelInfo, "%s", fmt.Sprint(args...)) }

// Infof logs at INFO level with printf-style formatting.
func (l *Logger) Infof(format string, args ...interface{}) { l.log(LevelInfo, format, args...) }

// Warn logs at WARN level. Recoverable anomalies: retries, fallbacks,
// approaching limits, deprecation warnings.
func (l *Logger) Warn(args ...interface{}) { l.log(LevelWarn, "%s", fmt.Sprint(args...)) }

// Warnf logs at WARN level with printf-style formatting.
func (l *Logger) Warnf(format string, args ...interface{}) { l.log(LevelWarn, format, args...) }

// Error logs at ERROR level. Failures requiring attention: 5xx responses,
// broken connections, exhausted retries. Each ERROR should be actionable.
func (l *Logger) Error(args ...interface{}) { l.log(LevelError, "%s", fmt.Sprint(args...)) }

// Errorf logs at ERROR level with printf-style formatting.
func (l *Logger) Errorf(format string, args ...interface{}) { l.log(LevelError, format, args...) }

// Fatal logs at FATAL level, flushes the log file, and exits with code 1.
// Use only for unrecoverable errors where continuing would be dangerous
// or impossible: missing critical config, failed migrations.
func (l *Logger) Fatal(args ...interface{}) {
	l.log(LevelFatal, "%s", fmt.Sprint(args...))
	l.Close()
	os.Exit(1)
}

// Fatalf logs at FATAL level with printf-style formatting, then exits.
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.log(LevelFatal, format, args...)
	l.Close()
	os.Exit(1)
}

// Close flushes and closes the underlying log file, if any.
// Safe to call multiple times. Should be deferred in main():
//
//	log := logging.Default()
//	defer log.Close()
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.currentFile != nil {
		err := l.currentFile.Close()
		l.currentFile = nil
		return err
	}
	return nil
}

// WithStack returns a shallow copy of the logger that includes call-stack
// traces in every log line. The copy shares the same underlying file handle,
// so both loggers write to the same file. Useful for error-critical paths
// where knowing the exact call chain is valuable.
//
//	errLog := log.WithStack()
//	errLog.Errorf("connection lost: %v", err)
//	// Output: [2006-01-02 15:04:05.000][ERROR][handler.go:42 <- server.go:10] connection lost: ...
func (l *Logger) WithStack() *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()
	return &Logger{
		level:          l.level,
		enableStack:    true,
		enableFile:     l.enableFile,
		stdoutOnly:     l.stdoutOnly,
		streamTag:      l.streamTag,
		logDir:         l.logDir,
		filename:       l.filename,
		rotationPolicy: l.rotationPolicy,
		maxSizeBytes:   l.maxSizeBytes,
		stackDepth:     l.stackDepth,
		currentFile:    l.currentFile,
		currentDate:    l.currentDate,
		currentSize:    l.currentSize,
		writers:        l.writers,
	}
}
