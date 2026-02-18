package logging

import (
	"os"
	"testing"
	"time"
)

func TestDatePath(t *testing.T) {
	tests := []struct {
		time time.Time
		want string
	}{
		{time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC), "2026/01/05"},
		{time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), "2026/12/31"},
		{time.Date(2000, 2, 1, 0, 0, 0, 0, time.UTC), "2000/02/01"},
	}
	for _, tt := range tests {
		if got := DatePath(tt.time); got != tt.want {
			t.Errorf("DatePath(%v) = %q, want %q", tt.time, got, tt.want)
		}
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	// Clear relevant env vars.
	os.Unsetenv(EnvLogLevel)
	os.Unsetenv(EnvLogDir)
	os.Unsetenv(EnvLogMaxSize)
	os.Unsetenv(EnvLogStdoutOnly)

	cfg := ConfigFromEnv()

	if cfg.Level != LevelInfo {
		t.Errorf("default Level = %v, want INFO", cfg.Level)
	}
	if !cfg.EnableStack {
		t.Error("default EnableStack should be true")
	}
	if cfg.StreamTag != "BACK" {
		t.Errorf("default StreamTag = %q, want \"BACK\"", cfg.StreamTag)
	}
	if cfg.RotationPolicy != RotationDaily {
		t.Errorf("default RotationPolicy = %v, want RotationDaily", cfg.RotationPolicy)
	}
	if cfg.MaxSizeBytes != 10*1024*1024 {
		t.Errorf("default MaxSizeBytes = %d, want %d", cfg.MaxSizeBytes, 10*1024*1024)
	}
	if cfg.Filename != "back.log" {
		t.Errorf("default Filename = %q, want \"back.log\"", cfg.Filename)
	}
}

func TestConfigFromEnvLogLevel(t *testing.T) {
	tests := []struct {
		envVal string
		want   Level
	}{
		{"debug", LevelDebug},
		{"WARN", LevelWarn},
		{"trace", LevelTrace},
		{"error", LevelError},
	}
	for _, tt := range tests {
		os.Setenv(EnvLogLevel, tt.envVal)
		cfg := ConfigFromEnv()
		if cfg.Level != tt.want {
			t.Errorf("LOG_LEVEL=%q -> Level=%v, want %v", tt.envVal, cfg.Level, tt.want)
		}
	}
	os.Unsetenv(EnvLogLevel)
}

func TestConfigFromEnvLogDir(t *testing.T) {
	os.Setenv(EnvLogDir, "/custom/logs")
	defer os.Unsetenv(EnvLogDir)

	cfg := ConfigFromEnv()
	if cfg.LogDir != "/custom/logs" {
		t.Errorf("LogDir = %q, want \"/custom/logs\"", cfg.LogDir)
	}
}

func TestConfigFromEnvMaxSize(t *testing.T) {
	os.Setenv(EnvLogMaxSize, "5242880")
	defer os.Unsetenv(EnvLogMaxSize)

	cfg := ConfigFromEnv()
	if cfg.MaxSizeBytes != 5242880 {
		t.Errorf("MaxSizeBytes = %d, want 5242880", cfg.MaxSizeBytes)
	}
}

func TestConfigFromEnvMaxSizeInvalid(t *testing.T) {
	os.Setenv(EnvLogMaxSize, "not-a-number")
	defer os.Unsetenv(EnvLogMaxSize)

	cfg := ConfigFromEnv()
	// Should keep the default.
	if cfg.MaxSizeBytes != 10*1024*1024 {
		t.Errorf("MaxSizeBytes = %d, want default %d", cfg.MaxSizeBytes, 10*1024*1024)
	}
}

func TestConfigFromEnvStdoutOnly(t *testing.T) {
	os.Setenv(EnvLogStdoutOnly, "true")
	defer os.Unsetenv(EnvLogStdoutOnly)

	cfg := ConfigFromEnv()
	if !cfg.StdoutOnly {
		t.Error("StdoutOnly should be true when LOG_STDOUT_ONLY=true")
	}
	if cfg.EnableFile {
		t.Error("EnableFile should be false when StdoutOnly=true")
	}
}
