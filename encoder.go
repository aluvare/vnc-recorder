package main

// encoder.go implements a custom ffmpeg-based video encoder that receives
// VNC screen frames as PPM images via stdin and produces an H.264/MP4 file.
//
// This is a workaround for https://github.com/amitbet/vnc2video/issues/10.
// It replaces the upstream X264ImageEncoder with a version that:
//   - Uses per-instance buffers instead of globals (thread-safe, no data races).
//   - Automatically resizes the PPM conversion buffer on resolution changes.
//   - Provides a done channel for synchronising with the ffmpeg process exit.
//   - Uses a mutex to protect Close/Encode from concurrent access.
//
// Lifecycle:
//  1. newEncoder() creates the struct.
//  2. Run() is called in a goroutine — it starts ffmpeg and blocks until exit.
//  3. Encode() is called from the frame-capture goroutine to write frames.
//  4. Close() closes ffmpeg's stdin, signalling it to finish the file.
//  5. Wait() blocks until ffmpeg has actually exited (safe to upload the file).

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"

	vnc "github.com/amitbet/vnc2video"
	"github.com/amitbet/vnc2video/encoders"
	"github.com/saily/vnc-recorder/logging"
)

// X264ImageCustomEncoder wraps ffmpeg to encode a stream of PPM images into
// an H.264 MP4 file. It is not reusable: create a new instance for each
// output file.
//
// Thread safety: Encode and Close may be called concurrently from different
// goroutines. All mutable state is protected by mu.
type X264ImageCustomEncoder struct {
	// Embedded for interface compatibility with vnc2video.
	encoders.X264ImageEncoder

	// FFMpegBinPath is the absolute path to the ffmpeg binary.
	FFMpegBinPath string

	// Framerate is the output video framerate (frames per second).
	// Defaults to 12 if set to 0.
	Framerate int

	// ConstantRateFactor controls the quality/size tradeoff.
	// Lower values = higher quality, larger files. Range: 0-51.
	ConstantRateFactor int

	mu     sync.Mutex     // protects closed, input
	cmd    *exec.Cmd      // ffmpeg process
	input  io.WriteCloser // ffmpeg stdin pipe
	closed bool           // true after Close() has been called
	done   chan error      // receives the result of cmd.Run()
	log    *logging.Logger

	// Per-instance conversion buffer for RGBA -> RGB PPM conversion.
	// Replaces the old global convImage variable to eliminate data races
	// and handle resolution changes correctly.
	convBuf  []uint8
	convBufW int // width when convBuf was last allocated
	convBufH int // height when convBuf was last allocated
}

// newEncoder creates an encoder instance. The ffmpeg process is not started
// until Run() is called.
func newEncoder(log *logging.Logger, ffmpegPath string, framerate, crf int) *X264ImageCustomEncoder {
	return &X264ImageCustomEncoder{
		FFMpegBinPath:      ffmpegPath,
		Framerate:          framerate,
		ConstantRateFactor: crf,
		done:               make(chan error, 1),
		log:                log,
	}
}

// Init prepares the ffmpeg command and opens its stdin pipe.
// Called internally by Run(); should not be called directly.
func (enc *X264ImageCustomEncoder) Init(videoFileName string) {
	if enc.Framerate == 0 {
		enc.Framerate = 12
	}
	cmd := exec.Command(enc.FFMpegBinPath,
		"-f", "image2pipe",
		"-vcodec", "ppm",
		"-r", strconv.Itoa(enc.Framerate),
		"-an", // no audio
		"-y",  // overwrite output
		"-i", "-", // read from stdin
		"-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2", // ensure even dimensions for H.264
		"-vcodec", "libx264",
		"-preset", "veryfast",
		"-g", "250", // keyframe interval
		"-crf", strconv.Itoa(enc.ConstantRateFactor),
		"-pix_fmt", "yuv420p", // compatibility with most players
		videoFileName,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	encInput, err := cmd.StdinPipe()
	if err != nil {
		enc.log.Errorf("failed to get ffmpeg stdin pipe: %v", err)
		return
	}
	enc.input = encInput
	enc.cmd = cmd
}

// Run starts the ffmpeg process and blocks until it exits. The exit error
// (or nil) is sent to the done channel so that Wait() can retrieve it.
//
// Must be called in a goroutine:
//
//	go vcodec.Run("output.mp4")
func (enc *X264ImageCustomEncoder) Run(videoFileName string) error {
	if _, err := os.Stat(enc.FFMpegBinPath); os.IsNotExist(err) {
		enc.done <- err
		return err
	}

	enc.Init(videoFileName)
	enc.log.Infof("launching ffmpeg: %v", enc.cmd.Args)
	err := enc.cmd.Run()
	if err != nil {
		enc.log.Debugf("ffmpeg exited: %v", err)
	}
	enc.done <- err
	return err
}

// Wait blocks until the ffmpeg process has exited. Call this after Close()
// to ensure the output file is complete before uploading or processing it.
func (enc *X264ImageCustomEncoder) Wait() error {
	return <-enc.done
}

// Encode writes a single frame to ffmpeg's stdin in PPM format.
// Returns nil immediately if the encoder has been closed.
// Thread-safe: may be called concurrently with Close.
func (enc *X264ImageCustomEncoder) Encode(img image.Image) error {
	enc.mu.Lock()
	if enc.input == nil || enc.closed {
		enc.mu.Unlock()
		return nil
	}
	input := enc.input
	enc.mu.Unlock()

	return enc.encodePPM(input, img)
}

// Close closes ffmpeg's stdin pipe, signalling it to finish writing the
// output file and exit. Thread-safe; safe to call multiple times.
//
// After calling Close, call Wait() to block until ffmpeg has fully exited.
func (enc *X264ImageCustomEncoder) Close() {
	enc.mu.Lock()
	if enc.closed {
		enc.mu.Unlock()
		return
	}
	enc.closed = true
	input := enc.input
	enc.mu.Unlock()

	if input != nil {
		if err := input.Close(); err != nil {
			enc.log.Debugf("closing ffmpeg stdin: %v", err)
		}
	}
}

// IsClosed returns whether the encoder has been closed. Thread-safe.
func (enc *X264ImageCustomEncoder) IsClosed() bool {
	enc.mu.Lock()
	defer enc.mu.Unlock()
	return enc.closed
}

// --- PPM encoding ---
//
// PPM (Portable Pixmap, P6 binary) is used as the intermediate format between
// VNC frames and ffmpeg because it is trivial to generate from raw pixel data
// with no compression overhead.

// encodePPM dispatches to the optimal PPM encoder based on the concrete image
// type: RGBImage (vnc2video native), RGBA (Go standard), or generic fallback.
func (enc *X264ImageCustomEncoder) encodePPM(w io.Writer, img image.Image) error {
	if img == nil {
		return errors.New("nil image")
	}
	switch concrete := img.(type) {
	case *vnc.RGBImage:
		return enc.encodePPMforRGBImage(w, concrete)
	case *image.RGBA:
		return enc.encodePPMforRGBA(w, concrete)
	default:
		return enc.encodePPMGeneric(w, img)
	}
}

// encodePPMforRGBA converts an RGBA image to PPM by stripping the alpha channel.
// Uses enc.convBuf as a reusable conversion buffer. The buffer is automatically
// reallocated when the image dimensions change (e.g. VNC resolution change).
func (enc *X264ImageCustomEncoder) encodePPMforRGBA(w io.Writer, img *image.RGBA) error {
	maxvalue := 255
	size := img.Bounds()
	if _, err := fmt.Fprintf(w, "P6\n%d %d\n%d\n", size.Dx(), size.Dy(), maxvalue); err != nil {
		return err
	}

	// Resize buffer if dimensions changed (handles VNC resolution changes).
	needed := size.Dy() * size.Dx() * 3
	if enc.convBuf == nil || len(enc.convBuf) != needed {
		enc.log.Debugf("allocating PPM buffer: %dx%d (%d bytes)", size.Dx(), size.Dy(), needed)
		enc.convBuf = make([]uint8, needed)
		enc.convBufW = size.Dx()
		enc.convBufH = size.Dy()
	}

	// Strip alpha channel: RGBA (4 bytes/pixel) -> RGB (3 bytes/pixel).
	rowCount := 0
	for i := 0; i < len(img.Pix); i++ {
		if (i % 4) != 3 { // skip every 4th byte (alpha)
			if rowCount < len(enc.convBuf) {
				enc.convBuf[rowCount] = img.Pix[i]
				rowCount++
			}
		}
	}

	_, err := w.Write(enc.convBuf[:rowCount])
	return err
}

// encodePPMGeneric handles any image.Image via the color.Model interface.
// Slower than the specialised methods but works with any image type.
func (enc *X264ImageCustomEncoder) encodePPMGeneric(w io.Writer, img image.Image) error {
	maxvalue := 255
	size := img.Bounds()
	if _, err := fmt.Fprintf(w, "P6\n%d %d\n%d\n", size.Dx(), size.Dy(), maxvalue); err != nil {
		return err
	}

	colModel := color.RGBAModel
	row := make([]uint8, size.Dx()*3)
	for y := size.Min.Y; y < size.Max.Y; y++ {
		i := 0
		for x := size.Min.X; x < size.Max.X; x++ {
			c := colModel.Convert(img.At(x, y)).(color.RGBA)
			row[i] = c.R
			row[i+1] = c.G
			row[i+2] = c.B
			i += 3
		}
		if _, err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// encodePPMforRGBImage writes a vnc2video RGBImage directly as PPM.
// This is the fastest path since the pixel data is already in RGB format.
func (enc *X264ImageCustomEncoder) encodePPMforRGBImage(w io.Writer, img *vnc.RGBImage) error {
	maxvalue := 255
	size := img.Bounds()
	if _, err := fmt.Fprintf(w, "P6\n%d %d\n%d\n", size.Dx(), size.Dy(), maxvalue); err != nil {
		return err
	}
	_, err := w.Write(img.Pix)
	return err
}
