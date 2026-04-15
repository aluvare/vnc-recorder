package main

// upload.go handles S3-compatible object storage uploads for recorded video files.
//
// Design decisions:
//   - Retry with exponential backoff: transient S3 failures don't lose recordings.
//   - Failed uploads keep the file on disk (never delete a file that wasn't uploaded).
//   - Each upload attempt has a 5-minute timeout to avoid hanging indefinitely.
//   - Bucket names support date placeholders ({YEAR}, {MONTH}, {DAY}) for
//     automatic daily partitioning.
//   - Upload is goroutine-safe and intended to be called with `go uploader.upload(...)`.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/aluvare/vnc-recorder/logging"
	"github.com/urfave/cli/v2"
)

const (
	// uploadMaxRetries is the maximum number of upload attempts before giving up.
	// After exhausting retries, the file is kept on disk for manual recovery.
	uploadMaxRetries = 3

	// uploadBaseBackoff is the initial wait time between retry attempts.
	// Each subsequent retry doubles the wait: 5s, 10s, 20s.
	uploadBaseBackoff = 5 * time.Second
)

// s3Uploader manages uploads to an S3-compatible object store.
// Create one instance via newS3Uploader and reuse it for all uploads.
type s3Uploader struct {
	client     *minio.Client
	bucketName string // may contain {YEAR}, {MONTH}, {DAY} placeholders
	region     string
	log        *logging.Logger
}

// newS3Uploader creates an S3 upload client from CLI flags. It validates
// the connection parameters but does not check bucket existence yet
// (that happens on first upload).
func newS3Uploader(c *cli.Context, log *logging.Logger) (*s3Uploader, error) {
	client, err := minio.New(c.String("s3_endpoint"), &minio.Options{
		Creds:  credentials.NewStaticV4(c.String("s3_accessKeyID"), c.String("s3_secretAccessKey"), ""),
		Secure: c.Bool("s3_ssl"),
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}

	log.Infof("S3 client configured: endpoint=%s bucket=%s ssl=%v",
		c.String("s3_endpoint"), c.String("s3_bucketName"), c.Bool("s3_ssl"))

	return &s3Uploader{
		client:     client,
		bucketName: c.String("s3_bucketName"),
		region:     c.String("s3_region"),
		log:        log,
	}, nil
}

// upload uploads a video file to S3 with retry logic and exponential backoff.
// On success, the local file is deleted. On failure after all retries, the
// file is kept on disk and an ERROR is logged.
//
// Safe to call from a goroutine:
//
//	go uploader.upload(outfileName)
func (u *s3Uploader) upload(outfileName string) {
	filePath := outfileName + ".mp4"
	bucket := procBucketName(u.bucketName)

	for attempt := 1; attempt <= uploadMaxRetries; attempt++ {
		err := u.tryUpload(bucket, filePath)
		if err == nil {
			u.log.Infof("S3 upload successful: %s -> %s/%s", filePath, bucket, filePath)
			if removeErr := os.Remove(filePath); removeErr != nil {
				u.log.Warnf("failed to remove uploaded file %s: %v", filePath, removeErr)
			}
			return
		}

		if attempt < uploadMaxRetries {
			backoff := uploadBaseBackoff * time.Duration(1<<(attempt-1))
			u.log.Warnf("S3 upload attempt %d/%d failed: %v, retrying in %v",
				attempt, uploadMaxRetries, err, backoff)
			time.Sleep(backoff)
		} else {
			u.log.Errorf("S3 upload failed after %d attempts: %v, file kept at %s",
				uploadMaxRetries, err, filePath)
		}
	}
}

// tryUpload performs a single upload attempt with a 5-minute timeout.
// It ensures the bucket exists before uploading.
func (u *s3Uploader) tryUpload(bucket, filePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := u.ensureBucket(ctx, bucket); err != nil {
		return fmt.Errorf("ensure bucket: %w", err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	fileStat, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}

	_, err = u.client.PutObject(ctx, bucket, filePath, file, fileStat.Size(), minio.PutObjectOptions{
		ContentType: "video/mp4",
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}

	return nil
}

// ensureBucket checks if the target bucket exists and creates it if not.
func (u *s3Uploader) ensureBucket(ctx context.Context, bucket string) error {
	found, err := u.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("bucket exists check: %w", err)
	}
	if !found {
		u.log.Infof("creating S3 bucket: %s (region=%s)", bucket, u.region)
		if err := u.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: u.region}); err != nil {
			return fmt.Errorf("make bucket: %w", err)
		}
	}
	return nil
}

// procBucketName replaces date placeholders in a bucket name template:
//
//	{YEAR}  -> 2026
//	{MONTH} -> 02
//	{DAY}   -> 17
//
// This allows automatic daily bucket partitioning, e.g.
// "recordings-{YEAR}-{MONTH}" -> "recordings-2026-02".
func procBucketName(s string) string {
	now := time.Now()
	s = strings.ReplaceAll(s, "{YEAR}", fmt.Sprintf("%d", now.Year()))
	s = strings.ReplaceAll(s, "{MONTH}", fmt.Sprintf("%02d", int(now.Month())))
	s = strings.ReplaceAll(s, "{DAY}", fmt.Sprintf("%02d", now.Day()))
	return s
}
