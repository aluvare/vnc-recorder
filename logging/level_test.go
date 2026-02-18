package logging

import "testing"

func TestLevelString(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelTrace, "TRACE"},
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelFatal, "FATAL"},
		{Level(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("Level(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestLevelOrdering(t *testing.T) {
	if LevelTrace >= LevelDebug {
		t.Error("TRACE should be less than DEBUG")
	}
	if LevelDebug >= LevelInfo {
		t.Error("DEBUG should be less than INFO")
	}
	if LevelInfo >= LevelWarn {
		t.Error("INFO should be less than WARN")
	}
	if LevelWarn >= LevelError {
		t.Error("WARN should be less than ERROR")
	}
	if LevelError >= LevelFatal {
		t.Error("ERROR should be less than FATAL")
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  Level
	}{
		{"trace", LevelTrace},
		{"TRACE", LevelTrace},
		{"  Trace  ", LevelTrace},
		{"debug", LevelDebug},
		{"DEBUG", LevelDebug},
		{"info", LevelInfo},
		{"INFO", LevelInfo},
		{"warn", LevelWarn},
		{"WARN", LevelWarn},
		{"WARNING", LevelWarn},
		{"warning", LevelWarn},
		{"error", LevelError},
		{"ERROR", LevelError},
		{"fatal", LevelFatal},
		{"FATAL", LevelFatal},
		// Unrecognised input defaults to INFO.
		{"", LevelInfo},
		{"garbage", LevelInfo},
		{"VERBOSE", LevelInfo},
		{"critical", LevelInfo},
	}
	for _, tt := range tests {
		if got := ParseLevel(tt.input); got != tt.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
