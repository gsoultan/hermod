package file

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestGenericFileSource_PingS3NoSilentSuccess is the regression test for the
// S3 connectivity false positive. Previously the S3 backend fell through to the
// default branch in Ping and always returned nil, so a misconfigured S3 source
// reported "ok" even with an invalid bucket or credentials. The fix routes S3
// through a real ListObjectsV2 probe, so connectivity failures now surface as
// errors instead of silent success.
func TestGenericFileSource_PingS3NoSilentSuccess(t *testing.T) {
	tests := []struct {
		name    string
		cfg     GenericConfig
		wantErr string
	}{
		{
			name:    "missing bucket is rejected up front",
			cfg:     GenericConfig{Backend: BackendS3, S3Region: "us-east-1"},
			wantErr: "s3_bucket is required",
		},
		{
			name: "unreachable endpoint surfaces a failure",
			cfg: GenericConfig{
				Backend:     BackendS3,
				S3Region:    "us-east-1",
				S3Bucket:    "does-not-exist",
				S3Endpoint:  "http://127.0.0.1:1", // nothing listens here
				S3AccessKey: "key",
				S3SecretKey: "secret",
			},
			wantErr: "s3 connectivity test failed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			src := NewGenericFileSource(tc.cfg)
			err := src.Ping(ctx)
			if err == nil {
				t.Fatalf("expected an error for S3 backend, got nil (silent success regression)")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestGenericFileSource_PingHonorsContext verifies that the FTP and SFTP
// connectivity tests abort promptly when the caller's context is cancelled,
// rather than blocking for the full per-backend dial timeout. The registry runs
// these probes in a goroutine raced against the request deadline; if the dial
// ignored the context (as the previous ssh.Dial / context-less goftp.Dial did)
// the goroutine — and its socket — would linger long after the 15s request
// deadline fired, leaking resources under repeated probes.
func TestGenericFileSource_PingHonorsContext(t *testing.T) {
	tests := []struct {
		name string
		cfg  GenericConfig
	}{
		{
			name: "ftp dial honors context",
			// TEST-NET-1 (RFC 5737) is guaranteed non-routable, so a real dial
			// would block until the timeout; a cancelled context must win first.
			cfg: GenericConfig{Backend: BackendFTP, FTPAddr: "192.0.2.1:21"},
		},
		{
			name: "sftp dial honors context",
			cfg:  GenericConfig{Backend: BackendSFTP, FTPAddr: "192.0.2.1:22", FTPUser: "u", FTPPass: "p"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel() // already cancelled before the dial begins

			src := NewGenericFileSource(tc.cfg)
			start := time.Now()
			err := src.Ping(ctx)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatalf("expected an error from a cancelled connectivity test, got nil")
			}
			// The per-backend dial timeout is 5s; honoring the context must
			// return far sooner. Allow generous slack for slow CI.
			if elapsed > 2*time.Second {
				t.Fatalf("Ping ignored context cancellation: returned after %v", elapsed)
			}
		})
	}
}
