//go:build integration
// +build integration

package mongodb

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/user/hermod/internal/storage"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestMongo_WorkflowLease_AcquireRenewRelease(t *testing.T) {
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("integration: set MONGODB_URI to run")
	}

	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo connect: %v", err)
	}
	defer client.Disconnect(ctx)

	dbName := os.Getenv("MONGODB_DB")
	if dbName == "" {
		dbName = "hermod_test"
	}

	s := NewMongoStorage(client, dbName)
	if err := s.Init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Ensure collection exists (use client DB directly to avoid depending on internal fields)
	coll := client.Database(dbName).Collection("workflows")

	wf := storage.Workflow{ID: uuid.New().String(), Name: "wf1", Active: true}
	if _, err := coll.InsertOne(ctx, map[string]any{
		"_id":       wf.ID,
		"name":      wf.Name,
		"active":    true,
		"vhost":     "",
		"nodes":     []any{},
		"edges":     []any{},
		"status":    "",
		"worker_id": "",
	}); err != nil {
		t.Fatalf("insert wf: %v", err)
	}
	t.Cleanup(func() { _ = coll.Drop(ctx) })

	ok, err := s.AcquireWorkflowLease(ctx, wf.ID, "w1", 5)
	if err != nil || !ok {
		t.Fatalf("acquire w1: ok=%v err=%v", ok, err)
	}

	// Second owner cannot acquire while not expired
	ok, err = s.AcquireWorkflowLease(ctx, wf.ID, "w2", 5)
	if err != nil {
		t.Fatalf("acquire w2 err: %v", err)
	}
	if ok {
		t.Fatalf("acquire by w2 should fail while lease active")
	}

	// Renew by owner
	ok, err = s.RenewWorkflowLease(ctx, wf.ID, "w1", 5)
	if err != nil || !ok {
		t.Fatalf("renew w1: ok=%v err=%v", ok, err)
	}

	// Force expiry by setting lease_until to past
	past := time.Now().Add(-1 * time.Minute)
	_, err = coll.UpdateByID(ctx, wf.ID, map[string]any{"$set": map[string]any{"lease_until": past}})
	if err != nil {
		t.Fatalf("force expire: %v", err)
	}

	// Now w2 can steal
	ok, err = s.AcquireWorkflowLease(ctx, wf.ID, "w2", 5)
	if err != nil || !ok {
		t.Fatalf("steal by w2: ok=%v err=%v", ok, err)
	}

	// Release by w2
	if err := s.ReleaseWorkflowLease(ctx, wf.ID, "w2"); err != nil {
		t.Fatalf("release w2: %v", err)
	}
}
