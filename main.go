// Package main implements vnc-recorder, a tool that connects to a VNC server
// and records the screen to an MP4 video file using ffmpeg.
//
// Architecture:
//
//	main() -> recorder() -> runRecordingSession()
//	                 ^--- reconnection loop with exponential backoff
//
//	runRecordingSession():
//	  1. TCP connect + VNC handshake
//	  2. Start ffmpeg encoder (goroutine)
//	  3. Start frame capture (goroutine)
//	  4. Main select loop: VNC events, encoder errors, split ticks, signals
//
// Error handling strategy:
//   - VNC disconnections: return error to outer loop, reconnect with backoff.
//   - Resolution changes: detected via EOF, treated as reconnectable error.
//   - Encoder errors: close session, upload partial file, reconnect.
//   - S3 upload failures: retried in background (see upload.go), never block recording.
//   - Signals (SIGINT/SIGTERM/SIGHUP/SIGQUIT): graceful shutdown via context cancellation.
//
// Split mode:
//   When --splitfile > 0, the output file is rotated every N minutes.
//   The VNC connection is kept alive; only the encoder is swapped.
//   Old files are uploaded in the background.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	vnc "github.com/amitbet/vnc2video"
	"github.com/saily/vnc-recorder/logging"
	"github.com/urfave/cli/v2"
)

const version = "0.5.0"

// log is the package-level logger, initialised in recorder().
var log *logging.Logger

func main() {
	app := &cli.App{
		Name:    path.Base(os.Args[0]),
		Usage:   "Connect to a vnc server and record the screen to a video.",
		Version: version,
		Authors: []*cli.Author{
			{
				Name:  "Daniel Widerin",
				Email: "daniel@widerin.net",
			},
		},
		Action: recorder,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "ffmpeg",
				Value:   "ffmpeg",
				Usage:   "Which ffmpeg executable to use",
				EnvVars: []string{"VR_FFMPEG_BIN"},
			},
			&cli.StringFlag{
				Name:    "host",
				Value:   "localhost",
				Usage:   "VNC host",
				EnvVars: []string{"VR_VNC_HOST"},
			},
			&cli.IntFlag{
				Name:    "port",
				Value:   5900,
				Usage:   "VNC port",
				EnvVars: []string{"VR_VNC_PORT"},
			},
			&cli.StringFlag{
				Name:    "password",
				Value:   "",
				Usage:   "Password to connect to the VNC host",
				EnvVars: []string{"VR_VNC_PASSWORD"},
			},
			&cli.IntFlag{
				Name:    "framerate",
				Value:   30,
				Usage:   "Framerate to record",
				EnvVars: []string{"VR_FRAMERATE"},
			},
			&cli.IntFlag{
				Name:    "crf",
				Value:   35,
				Usage:   "Constant Rate Factor (CRF) to record with",
				EnvVars: []string{"VR_CRF"},
			},
			&cli.StringFlag{
				Name:    "outfile",
				Value:   "output",
				Usage:   "Output file to record to.",
				EnvVars: []string{"VR_OUTFILE"},
			},
			&cli.IntFlag{
				Name:    "splitfile",
				Value:   0,
				Usage:   "Minutes to split file.",
				EnvVars: []string{"VR_SPLIT_OUTFILE"},
			},
			&cli.StringFlag{
				Name:    "s3_endpoint",
				Value:   "",
				Usage:   "S3 endpoint.",
				EnvVars: []string{"VR_S3_ENDPOINT"},
			},
			&cli.StringFlag{
				Name:    "s3_accessKeyID",
				Value:   "",
				Usage:   "S3 access key id.",
				EnvVars: []string{"VR_S3_ACCESSKEY"},
			},
			&cli.StringFlag{
				Name:    "s3_secretAccessKey",
				Value:   "",
				Usage:   "S3 secret access key.",
				EnvVars: []string{"VR_S3_SECRETACCESSKEY"},
			},
			&cli.StringFlag{
				Name:    "s3_bucketName",
				Value:   "",
				Usage:   "S3 bucket name.",
				EnvVars: []string{"VR_S3_BUCKETNAME"},
			},
			&cli.StringFlag{
				Name:    "s3_region",
				Value:   "us-east-1",
				Usage:   "S3 region.",
				EnvVars: []string{"VR_S3_REGION"},
			},
			&cli.BoolFlag{
				Name:    "s3_ssl",
				Value:   false,
				Usage:   "S3 SSL.",
				EnvVars: []string{"VR_S3_SSL"},
			},
			&cli.BoolFlag{
				Name:    "debug",
				Value:   false,
				Usage:   "Enable debug logging.",
				EnvVars: []string{"VR_DEBUG"},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		if log != nil {
			log.Errorf("recording failed: %v", err)
		} else {
			fmt.Fprintf(os.Stderr, "recording failed: %v\n", err)
		}
		os.Exit(1)
	}
}

// recorder is the main entry point called by the CLI framework.
// It initialises logging, validates configuration, sets up signal handling,
// and runs the recording loop with automatic reconnection.
func recorder(c *cli.Context) error {
	log = logging.Default()
	defer log.Close()

	if c.Bool("debug") {
		log.SetLevel(logging.LevelDebug)
	}

	log.Infof("vnc-recorder starting (version=%s)", version)

	// Validate ffmpeg availability before attempting any recording.
	ffmpegPath, err := exec.LookPath(c.String("ffmpeg"))
	if err != nil {
		return fmt.Errorf("ffmpeg binary not found: %w", err)
	}
	log.Infof("ffmpeg found: %s", ffmpegPath)

	// S3 setup (optional — only when s3_endpoint is configured).
	var uploader *s3Uploader
	if c.String("s3_endpoint") != "" {
		if c.Int("splitfile") == 0 {
			return fmt.Errorf("S3 upload requires --splitfile > 0")
		}
		uploader, err = newS3Uploader(c, log)
		if err != nil {
			return fmt.Errorf("S3 setup failed: %w", err)
		}
	}

	// Graceful shutdown: signals cancel the context, which propagates to
	// all recording goroutines without using panic or os.Exit.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-sigCh
		log.Infof("signal received: %v, initiating shutdown", sig)
		cancel()
	}()

	// Reconnection loop with exponential backoff.
	// On each session failure, we wait progressively longer before retrying
	// (1s -> 2s -> 4s -> ... -> 30s max). Backoff resets on successful sessions.
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		// Check for cancellation before starting a new session.
		select {
		case <-ctx.Done():
			log.Info("shutdown complete")
			return nil
		default:
		}

		err := runRecordingSession(ctx, c, ffmpegPath, uploader)
		if ctx.Err() != nil {
			log.Info("shutdown complete")
			return nil
		}

		if err != nil {
			log.Warnf("recording session ended: %v, reconnecting in %v", err, backoff)
			select {
			case <-time.After(backoff):
				backoff = minDuration(backoff*2, maxBackoff)
			case <-ctx.Done():
				log.Info("shutdown complete")
				return nil
			}
		} else {
			// Successful session (shouldn't normally happen) — reset backoff.
			backoff = time.Second
		}
	}
}

// runRecordingSession handles a single VNC connection lifetime:
// connect, negotiate, record, and return on error or cancellation.
//
// In split mode, the VNC connection is kept alive while output files
// are rotated every N minutes. The encoder is swapped without reconnecting.
func runRecordingSession(ctx context.Context, c *cli.Context, ffmpegPath string, uploader *s3Uploader) error {
	address := net.JoinHostPort(c.String("host"), fmt.Sprintf("%d", c.Int("port")))

	// --- TCP connection ---
	log.Infof("connecting to VNC: %s", address)
	dialer, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		return fmt.Errorf("TCP connection to %s failed: %w", address, err)
	}
	defer dialer.Close()
	log.Infof("TCP connection established: %s", address)

	// --- VNC handshake ---
	cchServer := make(chan vnc.ServerMessage)
	cchClient := make(chan vnc.ClientMessage)
	errorCh := make(chan error)

	// Password handling: empty string means no auth.
	// Fixed: old code compared against default "secret", which meant
	// users who genuinely wanted "secret" as their password couldn't use it.
	password := c.String("password")
	var secHandlers []vnc.SecurityHandler
	if password == "" {
		secHandlers = []vnc.SecurityHandler{
			&vnc.ClientAuthNone{},
		}
		log.Debug("using VNC auth: none")
	} else {
		secHandlers = []vnc.SecurityHandler{
			&vnc.ClientAuthVNC{Password: []byte(password)},
		}
		log.Debug("using VNC auth: password")
	}

	ccflags := &vnc.ClientConfig{
		SecurityHandlers: secHandlers,
		DrawCursor:       true,
		PixelFormat:      vnc.PixelFormat32bit,
		ClientMessageCh:  cchClient,
		ServerMessageCh:  cchServer,
		Messages:         vnc.DefaultServerMessages,
		Encodings: []vnc.Encoding{
			&vnc.RawEncoding{},
			&vnc.TightEncoding{},
			&vnc.HextileEncoding{},
			&vnc.ZRLEEncoding{},
			&vnc.CopyRectEncoding{},
			&vnc.CursorPseudoEncoding{},
			&vnc.CursorPosPseudoEncoding{},
			&vnc.ZLibEncoding{},
			&vnc.RREEncoding{},
		},
		ErrorCh: errorCh,
	}

	// Fixed: old code had `defer vncConn.Close()` BEFORE the error check,
	// causing a nil pointer dereference if Connect failed.
	vncConn, err := vnc.Connect(ctx, dialer, ccflags)
	if err != nil {
		return fmt.Errorf("VNC negotiation with %s failed: %w", address, err)
	}
	defer vncConn.Close()
	log.Infof("VNC session established: %s (%dx%d)", address, vncConn.Width(), vncConn.Height())

	screenImage := vncConn.Canvas

	// Configure encodings on the VNC canvas for rendering.
	for _, enc := range ccflags.Encodings {
		if renderer, ok := enc.(vnc.Renderer); ok {
			renderer.SetTargetImage(screenImage)
		}
	}

	// Tell the VNC server which encodings we prefer (order matters).
	vncConn.SetEncodings([]vnc.EncodingType{
		vnc.EncCursorPseudo,
		vnc.EncPointerPosPseudo,
		vnc.EncCopyRect,
		vnc.EncTight,
		vnc.EncZRLE,
		vnc.EncHextile,
		vnc.EncZlib,
		vnc.EncRRE,
	})

	// --- Encoder setup ---
	splitMode := c.Int("splitfile") > 0
	outfileName := generateOutfileName(c.String("outfile"), splitMode)
	vcodec := newEncoder(log, ffmpegPath, c.Int("framerate"), c.Int("crf"))

	// Start ffmpeg in a goroutine (blocks until the process exits).
	go vcodec.Run(outfileName + ".mp4")

	// Frame capture goroutine: reads the VNC canvas and writes PPM frames
	// to ffmpeg at the configured framerate.
	encoderErrCh := make(chan error, 1)
	stopEncoding := make(chan struct{})
	go encodeFrames(vcodec, screenImage, stopEncoding, encoderErrCh)

	// Split ticker: fires every N minutes to rotate the output file.
	// A nil channel (<-chan time.Time) in a select is never ready, which is
	// exactly what we want when split mode is disabled.
	var splitTicker *time.Ticker
	var splitTickerCh <-chan time.Time
	if splitMode {
		splitTicker = time.NewTicker(time.Duration(c.Int("splitfile")) * time.Minute)
		defer splitTicker.Stop()
		splitTickerCh = splitTicker.C
	}

	// Framebuffer update stats (logged at TRACE level).
	frameBufferReq := 0
	statsStart := time.Now()

	// --- Main event loop ---
	for {
		select {
		case <-ctx.Done():
			// Graceful shutdown: close encoder, wait for ffmpeg, upload.
			close(stopEncoding)
			vcodec.Close()
			vcodec.Wait()
			if uploader != nil {
				uploader.upload(outfileName)
			}
			return nil

		case err := <-errorCh:
			// VNC connection error. Close everything and return to the
			// reconnection loop. Resolution changes (EOF) are handled
			// the same way: reconnect to get the new resolution.
			close(stopEncoding)
			vcodec.Close()
			vcodec.Wait()
			if uploader != nil {
				go uploader.upload(outfileName)
			}
			if isResolutionChange(err) {
				log.Warnf("resolution change detected, will reconnect: %v", err)
			} else {
				log.Errorf("VNC connection error: %v", err)
			}
			return fmt.Errorf("VNC error: %w", err)

		case err := <-encoderErrCh:
			// Encoder (ffmpeg) error. Save what we can and reconnect.
			close(stopEncoding)
			vcodec.Close()
			vcodec.Wait()
			if uploader != nil {
				go uploader.upload(outfileName)
			}
			log.Errorf("encoder error: %v", err)
			return fmt.Errorf("encoder error: %w", err)

		case <-splitTickerCh:
			// File rotation: close current encoder, start a new one.
			// The VNC connection stays alive — no reconnection needed.
			log.Infof("split interval reached, rotating output file")
			close(stopEncoding)
			vcodec.Close()
			vcodec.Wait()

			// Upload the completed file in the background.
			if uploader != nil {
				go uploader.upload(outfileName)
			}

			// Create new encoder for the next segment.
			outfileName = generateOutfileName(c.String("outfile"), true)
			vcodec = newEncoder(log, ffmpegPath, c.Int("framerate"), c.Int("crf"))
			go vcodec.Run(outfileName + ".mp4")

			// Restart frame capture with fresh channels.
			stopEncoding = make(chan struct{})
			encoderErrCh = make(chan error, 1)
			go encodeFrames(vcodec, screenImage, stopEncoding, encoderErrCh)

		case msg := <-cchClient:
			log.Debugf("client message: type=%d", msg.Type())

		case msg := <-cchServer:
			if msg.Type() == vnc.FramebufferUpdateMsgType {
				frameBufferReq++
				elapsed := time.Since(statsStart).Seconds()
				reqPerSec := float64(frameBufferReq) / elapsed
				log.Tracef("framebuffer update #%d (%.1f req/s)", frameBufferReq, reqPerSec)

				// Request the next framebuffer update from the server.
				reqMsg := vnc.FramebufferUpdateRequest{
					Inc: 1, X: 0, Y: 0,
					Width:  vncConn.Width(),
					Height: vncConn.Height(),
				}
				reqMsg.Write(vncConn)
			}
		}
	}
}

// encodeFrames runs in a goroutine, encoding VNC screen frames at the
// configured framerate. It stops when the stop channel is closed or
// when an encoding error occurs (sent to errCh).
func encodeFrames(vcodec *X264ImageCustomEncoder, screenImage *vnc.VncCanvas, stop <-chan struct{}, errCh chan<- error) {
	frameDuration := time.Second / time.Duration(vcodec.Framerate)

	for {
		select {
		case <-stop:
			return
		default:
		}

		timeStart := time.Now()

		if err := vcodec.Encode(screenImage.Image); err != nil {
			// Non-blocking send: if errCh is full, the error is already reported.
			select {
			case errCh <- err:
			default:
			}
			return
		}

		// Sleep for the remaining frame interval to maintain consistent framerate.
		elapsed := time.Since(timeStart)
		if wait := frameDuration - elapsed; wait > 0 {
			time.Sleep(wait)
		}
	}
}

// generateOutfileName creates an output filename, optionally appending a
// timestamp for split mode. Format: "base-YYYY-MM-DD-HH-MM".
func generateOutfileName(base string, withTimestamp bool) string {
	if !withTimestamp {
		return base
	}
	t := time.Now()
	return fmt.Sprintf("%s-%04d-%02d-%02d-%02d-%02d",
		base, t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute())
}

// isResolutionChange detects VNC resolution changes, which manifest as
// EOF errors from the VNC library when the framebuffer size changes.
func isResolutionChange(err error) bool {
	return strings.Contains(err.Error(), "EOF")
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
