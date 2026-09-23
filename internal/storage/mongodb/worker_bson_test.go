package mongodb

import (
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ---------------------------------------------------------------------------
// The keys the workers collection is written with have to be the keys
// storage.Worker reads back.
//
// They were not. The driver's default field name is the lowercased Go name, so
// an untagged Worker decoded "lastseen", "cpuusage" and "memoryusage" while
// every write — CreateWorker, UpdateWorker, UpdateWorkerHeartbeat — set
// "last_seen", "cpu_usage" and "memory_usage". Nothing failed; the fields
// simply came back zero, so on this backend every worker read as never having
// heartbeated, which the UI renders as permanently offline.
//
// No server needed: the defect is entirely in the struct tags, and the same
// codec runs here as in the driver.
// ---------------------------------------------------------------------------

func TestWorkerDecodesTheKeysTheWritesUse(t *testing.T) {
	seen := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)

	// Spelled out rather than marshalled from a Worker, so this fails if the
	// tags and the write paths drift apart in either direction.
	doc := bson.M{
		"_id":                 "w1",
		"name":                "worker-1",
		"host":                "10.0.0.1",
		"port":                8080,
		"description":         "primary",
		"token":               "secret",
		"last_seen":           seen,
		"cpu_usage":           0.25,
		"memory_usage":        0.5,
		"cpu_cores":           int32(8),
		"memory_total_bytes":  int64(32 << 30),
		"memory_used_bytes":   int64(16 << 30),
		"storage_total_bytes": int64(500 << 30),
		"storage_used_bytes":  int64(125 << 30),
		"created_at":          seen,
	}

	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("marshalling the stored shape: %v", err)
	}

	// The same wrapper ListWorkers and GetWorker decode into.
	var got struct {
		storage.Worker `bson:",inline"`
		ID             string `bson:"_id"`
	}
	if err := bson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if got.LastSeen == nil {
		t.Fatal("last_seen did not decode — every worker reads as offline")
	}
	if !got.LastSeen.UTC().Equal(seen) {
		t.Errorf("LastSeen = %v, want %v", got.LastSeen.UTC(), seen)
	}

	want := storage.WorkerResources{
		CPUUsage:          0.25,
		MemoryUsage:       0.5,
		CPUCores:          8,
		MemoryTotalBytes:  32 << 30,
		MemoryUsedBytes:   16 << 30,
		StorageTotalBytes: 500 << 30,
		StorageUsedBytes:  125 << 30,
	}
	if got.WorkerResources != want {
		t.Errorf("resources:\n got %+v\nwant %+v", got.WorkerResources, want)
	}

	if got.Name != "worker-1" || got.Host != "10.0.0.1" || got.Port != 8080 {
		t.Errorf("identity fields did not decode: %+v", got.Worker)
	}
	if !got.CreatedAt.UTC().Equal(seen) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt.UTC(), seen)
	}
}

// Draining is set on API responses and never stored, so it must not be written
// back into the collection by a marshal of the same struct.
func TestWorkerDoesNotPersistDraining(t *testing.T) {
	raw, err := bson.Marshal(storage.Worker{ID: "w1", Draining: true})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var doc bson.M
	if err := bson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := doc["draining"]; ok {
		t.Errorf("draining reached the document: %v", doc)
	}
}
