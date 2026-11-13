// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package compression

import (
	"bytes"
	"errors"
	"runtime"
	"testing"
	"time"
)

// DecompressLimit is for bodies that arrive from someone else. Decompress
// inflates whatever it is handed; a few hundred bytes of zstd can declare
// gigabytes, so a caller that only checked the compressed size is not safe.
func TestDecompressLimitRefusesOutputPastTheLimit(t *testing.T) {
	plain := make([]byte, 4<<20) // compresses to almost nothing
	for _, algo := range []Algorithm{None, LZ4, Snappy, Zstd} {
		t.Run(string(algo), func(t *testing.T) {
			c, err := NewCompressor(algo)
			if err != nil {
				t.Fatal(err)
			}
			packed, err := c.Compress(plain)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := DecompressLimit(algo, packed, 1<<20); !errors.Is(err, ErrTooLarge) {
				t.Errorf("4 MiB against a 1 MiB limit: err = %v, want ErrTooLarge", err)
			}
			out, err := DecompressLimit(algo, packed, int64(len(plain)))
			if err != nil {
				t.Fatalf("exactly at the limit: %v", err)
			}
			if !bytes.Equal(out, plain) {
				t.Errorf("round trip at the limit lost data: got %d bytes", len(out))
			}
		})
	}
}

// A zstd decoder runs goroutines until it is closed. Decompress made a new
// one per call and never closed it.
func TestZstdDecompressDoesNotLeakGoroutines(t *testing.T) {
	c, _ := NewCompressor(Zstd)
	packed, _ := c.Compress([]byte("hello hermod"))

	before := runtime.NumGoroutine()
	for range 50 {
		if _, err := c.Decompress(packed); err != nil {
			t.Fatal(err)
		}
		if _, err := DecompressLimit(Zstd, packed, 1<<10); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines grew from %d to %d over 100 zstd decompressions", before, after)
	}
}
