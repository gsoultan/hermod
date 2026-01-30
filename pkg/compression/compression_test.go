package compression

import (
	"bytes"
	"testing"
)

func TestCompressors(t *testing.T) {
	testData := []byte("this is a test message that should be compressed and then decompressed correctly. It should be long enough to actually see some benefit from compression if we were measuring it, but for now we just want to ensure correctness.")

	algorithms := []Algorithm{LZ4, Snappy, Zstd}

	for _, algo := range algorithms {
		t.Run(string(algo), func(t *testing.T) {
			compressor, err := NewCompressor(algo)
			if err != nil {
				t.Fatalf("failed to create compressor for %s: %v", algo, err)
			}

			compressed, err := compressor.Compress(testData)
			if err != nil {
				t.Fatalf("failed to compress with %s: %v", algo, err)
			}

			// For very small or random data, compressed might be larger,
			// but for our testData it should probably be smaller or at least work.
			t.Logf("%s: original size: %d, compressed size: %d", algo, len(testData), len(compressed))

			decompressed, err := compressor.Decompress(compressed)
			if err != nil {
				t.Fatalf("failed to decompress with %s: %v", algo, err)
			}

			if !bytes.Equal(testData, decompressed) {
				t.Errorf("%s: decompressed data does not match original", algo)
			}
		})
	}
}

func TestEmptyData(t *testing.T) {
	algorithms := []Algorithm{LZ4, Snappy, Zstd, None}

	for _, algo := range algorithms {
		t.Run(string(algo), func(t *testing.T) {
			compressor, err := NewCompressor(algo)
			if err != nil {
				t.Fatalf("failed to create compressor for %s: %v", algo, err)
			}

			compressed, err := compressor.Compress([]byte{})
			if err != nil {
				t.Fatalf("failed to compress empty data with %s: %v", algo, err)
			}

			decompressed, err := compressor.Decompress(compressed)
			if err != nil {
				t.Fatalf("failed to decompress empty data with %s: %v", algo, err)
			}

			if len(decompressed) != 0 {
				t.Errorf("%s: expected empty decompressed data, got %d bytes", algo, len(decompressed))
			}
		})
	}
}
