package s3parquet

import (
	"testing"
)

func TestNewS3ParquetSink(t *testing.T) {
	sink, err := NewS3ParquetSink(t.Context(), "us-east-1", "bucket", "prefix", "ak", "sk", "", "{}", 4)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	if sink == nil {
		t.Fatal("sink is nil")
	}
}
