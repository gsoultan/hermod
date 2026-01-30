package s3parquet

import (
	"context"
	"testing"
)

func TestNewS3ParquetSink(t *testing.T) {
	sink, err := NewS3ParquetSink(context.Background(), "us-east-1", "bucket", "prefix", "ak", "sk", "", "{}", 4)
	if err != nil {
		t.Fatalf("failed to create sink: %v", err)
	}
	if sink == nil {
		t.Fatal("sink is nil")
	}
}
