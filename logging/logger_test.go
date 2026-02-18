package logging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newTestLogger creates a logger that writes to a buffer instead of stdout.
func newTestLogger(level Level, enableStack bool) (*Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	l := &Logger{
		level:          level,
		enableStack:    enableStack,
		enableFile:     false,
		stdoutOnly:     false,
		streamTag:      "",
		logDir:         "",
		filename:       "test.log",
		rotationPolicy: RotationNone,
		maxSizeBytes:   10 * 1024 * 1024,
		stackDepth:     10,
		writers:        []io.Writer{buf},
	}
	return l, buf
}

func TestLoggerLevelFiltering(t *testing.T) {
	l, buf := newTestLogger(LevelWarn, false)

	l.Trace("should not appear")
	l.Debug("should not appear")
	l.Info("should not appear")
	l.Warn("should appear")
	l.Error("should also appear")

	output := buf.String()
	if strings.Contains(output, "should not appear") {
		t.Error("messages below WARN level should be filtered out")
	}
	if !strings.Contains(output, "should appear") {
		t.Error("WARN message should appear")
	}
	if !strings.Contains(output, "should also appear") {
		t.Error("ERROR message should appear")
	}
}

func TestLoggerFormat(t *testing.T) {
	l, buf := newTestLogger(LevelInfo, false)

	l.Info("test message")

	output := buf.String()
	if !strings.Contains(output, "[INFO]") {
		t.Errorf("output missing [INFO]: %q", output)
	}
	if !strings.Contains(output, "test message") {
		t.Errorf("output missing message: %q", output)
	}
	if !strings.Contains(output, "[20") {
		t.Errorf("output missing timestamp: %q", output)
	}
}

func TestLoggerFormatf(t *testing.T) {
	l, buf := newTestLogger(LevelInfo, false)

	l.Infof("value=%d name=%s", 42, "test")

	output := buf.String()
	if !strings.Contains(output, "value=42 name=test") {
		t.Errorf("formatted output wrong: %q", output)
	}
}

func TestLoggerWithStack(t *testing.T) {
	l, _ := newTestLogger(LevelInfo, false)
	ls := l.WithStack()

	buf := &bytes.Buffer{}
	ls.writers = []io.Writer{buf}

	ls.Info("stack message")

	output := buf.String()
	if !strings.Contains(output, ".go:") {
		t.Errorf("WithStack output missing stack trace: %q", output)
	}
}

func TestLoggerStdoutOnlyTag(t *testing.T) {
	buf := &bytes.Buffer{}
	l := &Logger{
		level:      LevelInfo,
		stdoutOnly: true,
		streamTag:  "BACK",
		writers:    []io.Writer{buf},
	}

	l.Info("tagged message")

	output := buf.String()
	if !strings.HasPrefix(output, "[BACK]") {
		t.Errorf("stdout-only output should start with [BACK]: %q", output)
	}
}

func TestLoggerSetGetLevel(t *testing.T) {
	l, _ := newTestLogger(LevelInfo, false)

	if l.GetLevel() != LevelInfo {
		t.Errorf("initial level = %v, want INFO", l.GetLevel())
	}

	l.SetLevel(LevelDebug)
	if l.GetLevel() != LevelDebug {
		t.Errorf("after SetLevel = %v, want DEBUG", l.GetLevel())
	}
}

func TestLoggerConcurrency(t *testing.T) {
	l, _ := newTestLogger(LevelInfo, false)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			l.Infof("goroutine %d", n)
		}(i)
	}
	wg.Wait()
}

func TestLoggerFileRotation(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Level:          LevelInfo,
		EnableFile:     true,
		LogDir:         tmpDir,
		RotationPolicy: RotationDaily,
		Filename:       "test.log",
		StackDepth:     10,
	}
	l := New(cfg)
	defer l.Close()

	l.Info("rotation test")

	matches, err := filepath.Glob(filepath.Join(tmpDir, "*/*/*/test.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Error("log file not created in date directory")
	}

	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "rotation test") {
		t.Error("log file doesn't contain expected message")
	}
}

func TestLoggerClose(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Level:      LevelInfo,
		EnableFile: true,
		LogDir:     tmpDir,
		Filename:   "close-test.log",
		StackDepth: 10,
	}
	l := New(cfg)

	l.Info("before close")
	err := l.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}

	err = l.Close()
	if err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}

func TestLoggerAllLevels(t *testing.T) {
	l, buf := newTestLogger(LevelTrace, false)

	l.Trace("t")
	l.Debug("d")
	l.Info("i")
	l.Warn("w")
	l.Error("e")

	output := buf.String()
	for _, level := range []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR"} {
		if !strings.Contains(output, "["+level+"]") {
			t.Errorf("missing [%s] in output", level)
		}
	}
}
