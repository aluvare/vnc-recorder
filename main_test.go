package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGenerateOutfileName_NoTimestamp(t *testing.T) {
	name := generateOutfileName("output", false)
	if name != "output" {
		t.Errorf("generateOutfileName(\"output\", false) = %q, want \"output\"", name)
	}
}

func TestGenerateOutfileName_WithTimestamp(t *testing.T) {
	name := generateOutfileName("output", true)

	// Should start with "output-".
	if !strings.HasPrefix(name, "output-") {
		t.Errorf("expected prefix \"output-\", got %q", name)
	}

	// Should contain the current year.
	year := time.Now().Format("2006")
	if !strings.Contains(name, year) {
		t.Errorf("expected year %s in %q", year, name)
	}

	// Format: output-YYYY-MM-DD-HH-MM (5 dashes after "output-").
	parts := strings.Split(name, "-")
	if len(parts) != 6 {
		t.Errorf("expected 6 parts (base + 5 time components), got %d: %q", len(parts), name)
	}
}

func TestGenerateOutfileName_DifferentBase(t *testing.T) {
	name := generateOutfileName("recording", true)
	if !strings.HasPrefix(name, "recording-") {
		t.Errorf("expected prefix \"recording-\", got %q", name)
	}
}

func TestIsResolutionChange(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{errors.New("read tcp: EOF"), true},
		{errors.New("EOF"), true},
		{errors.New("connection reset by peer"), false},
		{errors.New("timeout"), false},
		{errors.New("something with EOF in the middle"), true},
	}
	for _, tt := range tests {
		if got := isResolutionChange(tt.err); got != tt.want {
			t.Errorf("isResolutionChange(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestMinDuration(t *testing.T) {
	tests := []struct {
		a, b time.Duration
		want time.Duration
	}{
		{time.Second, 2 * time.Second, time.Second},
		{5 * time.Second, 3 * time.Second, 3 * time.Second},
		{time.Minute, time.Minute, time.Minute},
		{0, time.Second, 0},
	}
	for _, tt := range tests {
		if got := minDuration(tt.a, tt.b); got != tt.want {
			t.Errorf("minDuration(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestProcBucketName(t *testing.T) {
	now := time.Now()
	result := procBucketName("recordings-{YEAR}-{MONTH}-{DAY}")

	if !strings.Contains(result, time.Now().Format("2006")) {
		t.Errorf("expected current year in %q", result)
	}
	if strings.Contains(result, "{YEAR}") {
		t.Errorf("placeholder {YEAR} not replaced in %q", result)
	}
	if strings.Contains(result, "{MONTH}") {
		t.Errorf("placeholder {MONTH} not replaced in %q", result)
	}
	if strings.Contains(result, "{DAY}") {
		t.Errorf("placeholder {DAY} not replaced in %q", result)
	}

	// No placeholders: should return unchanged.
	plain := procBucketName("my-bucket")
	if plain != "my-bucket" {
		t.Errorf("plain bucket name changed: %q", plain)
	}

	_ = now
}

func TestProcBucketNameFormat(t *testing.T) {
	now := time.Now()
	result := procBucketName("{YEAR}/{MONTH}/{DAY}")

	expected := now.Format("2006/01/02")
	if result != expected {
		t.Errorf("procBucketName format = %q, want %q", result, expected)
	}
}
