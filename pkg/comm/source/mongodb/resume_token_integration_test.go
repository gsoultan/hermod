//go:build integration

package mongodb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// The MongoDB CDC source, against a real replica set.
//
// What is being checked is the one thing a change-stream source has to get
// right: where it resumes after a restart. The engine persists source state
// from GetState() every time a message is acknowledged
// (registry_routing.go, statefulSource.Ack), so whatever GetState reports is
// the position the pipeline will come back to. If it reports a position ahead
// of what was acknowledged, the messages in between are not redelivered — they
// are gone, and nothing reports it.
//
// Change streams need a replica set, which a standalone mongod cannot serve at
// all. That is why this reads MONGODB_RS_URI rather than MONGODB_URI: the
// latter is set for every other MongoDB test and usually points at a
// standalone, and quietly reusing it would turn these into a skip on most
// machines.
//
//	container run -d --name mongo-dev -p 27017:27017 mongo:7 \
//	  mongod --replSet rs0 --bind_ip_all
//	container exec mongo-dev mongosh --eval 'rs.initiate()'
//	MONGODB_RS_URI=mongodb://localhost:27017/?directConnection=true

func requireReplicaSet(t *testing.T) (string, string) {
	t.Helper()
	uri := os.Getenv("MONGODB_RS_URI")
	if uri == "" || os.Getenv("HERMOD_INTEGRATION") != "1" {
		// In CI a replica set is started for exactly this, so a missing
		// endpoint there means the wiring broke and these clauses would report
		// success without running. Locally it just means no server.
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q MONGODB_RS_URI=%q in CI, where a replica set "+
				"is started for this test; the CDC source would go unexercised and the "+
				"run would still be green",
				os.Getenv("HERMOD_INTEGRATION"), uri)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MONGODB_RS_URI to run")
	}

	ctx := t.Context()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("MONGODB_URI names a server that could not be reached (%s): %v", uri, err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	var hello bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		t.Fatalf("hello against %s: %v", uri, err)
	}
	if _, ok := hello["setName"]; !ok {
		t.Fatalf("MONGODB_RS_URI (%s) names a standalone mongod; change streams need a "+
			"replica set, so this source cannot be exercised against it at all", uri)
	}

	db := fmt.Sprintf("hermod_cdc_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = client.Database(db).Drop(context.Background()) })
	return uri, db
}

func insertDocs(t *testing.T, uri, db, coll string, n int) {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	for i := range n {
		if _, err := client.Database(db).Collection(coll).
			InsertOne(t.Context(), bson.M{"seq": i}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
}

// Reading a message is not the same as having delivered it. Until it is
// acknowledged, the position a restart resumes from must not have moved past
// it, or the message is dropped by the restart.
func TestStateDoesNotAdvancePastUnacknowledgedMessages(t *testing.T) {
	uri, db := requireReplicaSet(t)
	const coll = "events"

	src := NewMongoDBSource(uri, db, coll, true)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Open the stream before writing, so the events are ones it watches.
	if err := src.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	insertDocs(t, uri, db, coll, 3)

	// Read all three and acknowledge none of them: this is a pipeline that has
	// pulled work and not yet finished it.
	for i := range 3 {
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if msg == nil {
			t.Fatalf("read %d returned no message", i)
		}
	}

	state := src.GetState()
	if token := state["resume_token"]; token != "" {
		t.Errorf("after reading 3 messages and acknowledging none, the source reports a "+
			"resume position (%s...)\n"+
			"the engine persists this on every ack, so a restart resumes past messages "+
			"that were read and never delivered, and they are not redelivered",
			token[:min(len(token), 24)])
	}
}

// The same thing stated as its consequence, which is the part that matters:
// restore the state a crash would have left behind, and see whether the work
// that was in flight comes back.
func TestUnacknowledgedMessagesAreRedeliveredAfterRestart(t *testing.T) {
	uri, db := requireReplicaSet(t)
	const coll = "events"

	first := NewMongoDBSource(uri, db, coll, true)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	if err := first.init(ctx); err != nil {
		t.Fatalf("init: %v", err)
	}

	insertDocs(t, uri, db, coll, 3)

	// Acknowledge the first only. Two remain in flight, as they would be when a
	// worker dies with messages between the source and the sink.
	msg, err := first.Read(ctx)
	if err != nil {
		t.Fatalf("read 0: %v", err)
	}
	if err := first.Ack(ctx, msg); err != nil {
		t.Fatalf("ack 0: %v", err)
	}
	for i := 1; i < 3; i++ {
		if _, err := first.Read(ctx); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}

	// This is the state the engine would have persisted, and what the
	// replacement worker starts from.
	state := first.GetState()
	_ = first.Close()

	second := NewMongoDBSource(uri, db, coll, true)
	second.SetState(state)
	t.Cleanup(func() { _ = second.Close() })

	readCtx, readCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer readCancel()

	var seqs []int32
	for i := range 2 {
		m, err := second.Read(readCtx)
		if err != nil {
			t.Fatalf("reading %d from the replacement source: %v", i, err)
		}
		if m == nil {
			t.Fatalf("reading %d from the replacement source returned no message", i)
		}
		// The source writes the document as extended JSON, not raw BSON.
		var doc bson.M
		if err := bson.UnmarshalExtJSON(m.After(), true, &doc); err != nil {
			t.Fatalf("decoding the payload of message %d (%q): %v", i, m.After(), err)
		}
		v, ok := doc["seq"].(int32)
		if !ok {
			t.Fatalf("message %d has no seq field: %v", i, doc)
		}
		seqs = append(seqs, v)
	}

	if len(seqs) != 2 {
		t.Errorf("after a restart with one message acknowledged of three, the replacement "+
			"source redelivered %d messages (%v), want the 2 that were never acknowledged\n"+
			"the resume position advanced when each message was read rather than when it "+
			"was acknowledged, so the work that was in flight is skipped and lost",
			len(seqs), seqs)
	}
}
