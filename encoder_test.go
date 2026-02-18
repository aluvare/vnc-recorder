package main

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"

	"github.com/saily/vnc-recorder/logging"
)

// newTestEncoderLogger creates a logger for encoder tests.
func newTestEncoderLogger() *logging.Logger {
	return logging.New(logging.Config{
		Level:      logging.LevelTrace,
		EnableFile: false,
		StdoutOnly: false,
		StackDepth: 10,
	})
}

func TestEncoderPPMGeneric(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	// Create a small 2x2 red image.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}

	var buf bytes.Buffer
	err := enc.encodePPMGeneric(&buf, img)
	if err != nil {
		t.Fatalf("encodePPMGeneric error: %v", err)
	}

	output := buf.String()
	// PPM header check.
	if !strings.HasPrefix(output, "P6\n2 2\n255\n") {
		t.Errorf("PPM header wrong: %q", output[:20])
	}

	// Body should be 2*2*3 = 12 bytes of pixel data.
	header := "P6\n2 2\n255\n"
	body := output[len(header):]
	if len(body) != 12 {
		t.Errorf("PPM body length = %d, want 12", len(body))
	}

	// All pixels should be red (255, 0, 0).
	for i := 0; i < len(body); i += 3 {
		if body[i] != 255 || body[i+1] != 0 || body[i+2] != 0 {
			t.Errorf("pixel at offset %d = (%d,%d,%d), want (255,0,0)",
				i, body[i], body[i+1], body[i+2])
		}
	}
}

func TestEncoderPPMforRGBA(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	// Create a 3x2 green image.
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.Set(x, y, color.RGBA{0, 255, 0, 255})
		}
	}

	var buf bytes.Buffer
	err := enc.encodePPMforRGBA(&buf, img)
	if err != nil {
		t.Fatalf("encodePPMforRGBA error: %v", err)
	}

	output := buf.Bytes()
	header := "P6\n3 2\n255\n"
	if !strings.HasPrefix(string(output), header) {
		t.Errorf("PPM header wrong")
	}

	// Body: 3*2*3 = 18 bytes.
	body := output[len(header):]
	if len(body) != 18 {
		t.Errorf("PPM body length = %d, want 18", len(body))
	}

	// Verify convBuf was allocated.
	if enc.convBuf == nil {
		t.Error("convBuf should have been allocated")
	}
	if enc.convBufW != 3 || enc.convBufH != 2 {
		t.Errorf("convBuf dimensions = %dx%d, want 3x2", enc.convBufW, enc.convBufH)
	}
}

func TestEncoderPPMforRGBA_ResolutionChange(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	// First encode: 2x2.
	img1 := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			img1.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	var buf1 bytes.Buffer
	if err := enc.encodePPMforRGBA(&buf1, img1); err != nil {
		t.Fatal(err)
	}
	if enc.convBufW != 2 || enc.convBufH != 2 {
		t.Errorf("after first encode: %dx%d, want 2x2", enc.convBufW, enc.convBufH)
	}

	// Second encode: 4x3 (resolution change).
	img2 := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img2.Set(x, y, color.RGBA{0, 0, 255, 255})
		}
	}
	var buf2 bytes.Buffer
	if err := enc.encodePPMforRGBA(&buf2, img2); err != nil {
		t.Fatal(err)
	}

	// Buffer should have been reallocated for the new size.
	if enc.convBufW != 4 || enc.convBufH != 3 {
		t.Errorf("after resolution change: %dx%d, want 4x3", enc.convBufW, enc.convBufH)
	}
	expectedBufLen := 4 * 3 * 3
	if len(enc.convBuf) != expectedBufLen {
		t.Errorf("convBuf length = %d, want %d", len(enc.convBuf), expectedBufLen)
	}
}

func TestEncoderPPMNilImage(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	var buf bytes.Buffer
	err := enc.encodePPM(&buf, nil)
	if err == nil {
		t.Error("encodePPM(nil) should return error")
	}
	if err.Error() != "nil image" {
		t.Errorf("error = %q, want \"nil image\"", err.Error())
	}
}

func TestEncoderCloseIdempotent(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	// Close without Init should not panic.
	enc.Close()
	enc.Close() // Second close should be safe.

	if !enc.IsClosed() {
		t.Error("IsClosed() should return true after Close()")
	}
}

func TestEncoderEncodeAfterClose(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)
	enc.Close()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	err := enc.Encode(img)
	if err != nil {
		t.Errorf("Encode after Close should return nil, got %v", err)
	}
}

func TestEncoderIsClosed(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	if enc.IsClosed() {
		t.Error("IsClosed() should be false initially")
	}
	enc.Close()
	if !enc.IsClosed() {
		t.Error("IsClosed() should be true after Close()")
	}
}

func TestEncoderConcurrentCloseEncode(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 30, 35)

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))

	var wg sync.WaitGroup
	// Run Encode and Close concurrently to check for data races.
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			enc.Encode(img)
		}()
		go func() {
			defer wg.Done()
			enc.Close()
		}()
	}
	wg.Wait()
	// No panic or data race = pass.
}

func TestEncoderDefaultFramerate(t *testing.T) {
	log := newTestEncoderLogger()
	enc := newEncoder(log, "/usr/bin/ffmpeg", 0, 35)

	// Init should set default framerate.
	enc.Init("/dev/null")
	if enc.Framerate != 12 {
		t.Errorf("default Framerate = %d, want 12", enc.Framerate)
	}
}
