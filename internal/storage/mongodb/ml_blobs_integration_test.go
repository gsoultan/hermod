//go:build integration
// +build integration

package mongodb

import (
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/mlblobtest"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_blobs_test.go),
// against a live MongoDB.
func TestMongoMLBlobs(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.MLBlobStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLBlobStore")
	}
	mlblobtest.Run(t, st, "tenant-a")
}
