//go:build integration

package mongodb

import (
	"context"
	"testing"
	"time"

	"github.com/user/hermod"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Initial load: carrying across the documents that were already in the
// collection when the workflow started.
//
// Without it a change stream is only a tail. It reports what happens next and
// says nothing about what is already there, so a new workflow against an
// existing collection delivers nothing until somebody touches a document. The
// PostgreSQL source has had this since it could export a snapshot with its
// replication slot; these clauses hold the MongoDB source to the same bargain.
//
// The bargain has three parts, and each is a separate failure if it breaks:
//
//  1. the existing documents arrive at all;
//  2. they arrive once, not on every restart;
//  3. a change made while the backfill is still running is not lost between
//     the two — which is the part that is easy to get wrong and silent when it
//     is wrong.
//
// Needs a replica set, for the same reason the resume-token tests do.

// drain reads until it has seen wantIDs distinct document ids or the context
// expires, returning what arrived. Reading by count rather than to exhaustion
// is deliberate: a change stream never ends, so a test that waits for EOF hangs
// until its deadline whether the source is right or wrong.
func drain(t *testing.T, src *MongoDBSource, ctx context.Context, want int) []bson.M {
	t.Helper()
	var docs []bson.M
	for len(docs) < want {
		msg, err := src.Read(ctx)
		if err != nil {
			// A deadline here means the messages never came, which is the
			// failure the caller is asserting about; return what we have and
			// let it report the shortfall with its own wording.
			return docs
		}
		if msg == nil {
			continue
		}
		var doc bson.M
		if err := bson.UnmarshalExtJSON(msg.After(), true, &doc); err != nil {
			t.Fatalf("decoding payload %q: %v", msg.After(), err)
		}
		docs = append(docs, doc)
	}
	return docs
}

// insertMarker writes one document distinguishable from everything insertDocs
// writes, so it can be recognised however the rest of the collection is
// ordered.
func insertMarker(t *testing.T, uri, db, coll string) {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	if _, err := client.Database(db).Collection(coll).
		InsertOne(t.Context(), bson.M{"marker": true}); err != nil {
		t.Fatalf("inserting the marker document: %v", err)
	}
}

func seqsOf(docs []bson.M) []int {
	var out []int
	for _, d := range docs {
		switch v := d["seq"].(type) {
		case int32:
			out = append(out, int(v))
		case int64:
			out = append(out, int(v))
		case float64:
			out = append(out, int(v))
		}
	}
	return out
}

// The documents that were already there when the source started.
func TestInitialLoadCarriesExistingDocuments(t *testing.T) {
	uri, db := requireReplicaSet(t)
	const coll = "events"

	// Written before the source exists, so a change stream alone cannot see
	// them: they are in the collection, not in the oplog tail it subscribes to.
	insertDocs(t, uri, db, coll, 5)

	src := NewMongoDBSource(uri, db, coll, true)
	src.SetInitialLoad(true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	docs := drain(t, src, ctx, 5)
	if len(docs) != 5 {
		t.Fatalf("initial load delivered %d of the 5 documents already in the collection %v\n"+
			"a change stream reports only what happens after it opens, so without a backfill "+
			"a workflow started against an existing collection moves nothing until somebody "+
			"writes to it",
			len(docs), seqsOf(docs))
	}
}

// Once, not on every restart. The persisted resume token is the source's record
// of having streamed before: if it has one, the documents are downstream
// already and re-reading the whole collection would be a duplicate storm on
// every worker restart.
func TestInitialLoadRunsOnlyOnce(t *testing.T) {
	uri, db := requireReplicaSet(t)
	const coll = "events"

	insertDocs(t, uri, db, coll, 3)

	first := NewMongoDBSource(uri, db, coll, true)
	first.SetInitialLoad(true)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Read and acknowledge all three, so the persisted state is that of a
	// source that has finished its backfill.
	for i := range 3 {
		msg, err := first.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if err := first.Ack(ctx, msg); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
	}
	state := first.GetState()
	_ = first.Close()

	// Not the resume token. Snapshot messages carry none — they did not come
	// from the stream — so acknowledging a backfill moves no stream position,
	// and a collection that is backfilled and then never written to would look
	// exactly like one that had never run. The completed backfill has to be its
	// own record, or it repeats on every restart.
	if state["initial_load"] != "done" {
		t.Fatalf("after acknowledging a completed backfill the source reports initial_load=%q, "+
			"want \"done\"\nnothing else distinguishes a first run from a resumed one, so the "+
			"whole collection is re-read on every restart",
			state["initial_load"])
	}

	// The replacement worker: same config, restored state.
	second := NewMongoDBSource(uri, db, coll, true)
	second.SetInitialLoad(true)
	second.SetState(state)
	t.Cleanup(func() { _ = second.Close() })

	// One new document, distinguishable from the three already carried across,
	// so there is something to wait for that proves the source is live rather
	// than merely silent.
	insertMarker(t, uri, db, coll)

	readCtx, readCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer readCancel()

	// What must not happen is a *re-run backfill*, which is a snapshot message.
	// Some overlap of change events is expected and is not the same thing:
	// with no acknowledged token the stream resumes from the cluster time the
	// backfill began at, and MongoDB's cluster time is second-granular, so a
	// write made in that same second can be reported again. That is a duplicate
	// of a document already carried across, which is the ordinary
	// at-least-once bargain and is collapsed by sink-side idempotency. A
	// snapshot message is not a duplicate of anything — it means the whole
	// collection is being read again on every restart.
	sawMarker := false
	for !sawMarker {
		msg, err := second.Read(readCtx)
		if err != nil {
			break
		}
		if msg == nil {
			continue
		}
		if msg.Operation() == hermod.OpSnapshot {
			t.Fatalf("the restarted source emitted a snapshot message for table %q\n"+
				"the completed backfill was recorded in state (initial_load=done) and the "+
				"source read it back, yet it read the collection again anyway: every restart "+
				"re-copies the whole collection",
				msg.Table())
		}
		var doc bson.M
		if err := bson.UnmarshalExtJSON(msg.After(), true, &doc); err != nil {
			continue
		}
		if v, ok := doc["marker"].(bool); ok && v {
			sawMarker = true
		}
	}

	if !sawMarker {
		t.Error("the document written after the restart never arrived; the restarted source " +
			"is not streaming")
	}
}

// The gap between the backfill and the tail.
//
// The stream has to be opened before the collection is read, not after. Open it
// after and every change made during the backfill falls in the hole between the
// two: too late for the snapshot to have seen it, too early for the stream to
// report it. Nothing errors, the counts all balance, and the row is simply not
// there.
func TestChangesDuringInitialLoadAreNotLost(t *testing.T) {
	uri, db := requireReplicaSet(t)
	const coll = "events"

	// Large enough that the backfill is still running after the first read:
	// the source buffer holds 64, so 500 documents cannot be in flight at once.
	const existing = 500
	insertDocs(t, uri, db, coll, existing)

	src := NewMongoDBSource(uri, db, coll, true)
	src.SetInitialLoad(true)
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	// One read is enough to know the backfill has started and, with 500
	// documents against a 64-message buffer, that it has not finished.
	if _, err := src.Read(ctx); err != nil {
		t.Fatalf("first read: %v", err)
	}

	// The write that lands in the gap.
	insertMarker(t, uri, db, coll)

	// Everything else the backfill owes, plus the marker.
	deadline, deadlineCancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer deadlineCancel()

	found := false
	for range existing + 50 {
		msg, err := src.Read(deadline)
		if err != nil {
			break
		}
		if msg == nil {
			continue
		}
		var doc bson.M
		if err := bson.UnmarshalExtJSON(msg.After(), true, &doc); err != nil {
			continue
		}
		if v, ok := doc["marker"].(bool); ok && v {
			found = true
			break
		}
	}

	if !found {
		t.Error("a document written while the initial load was still running never arrived\n" +
			"the change stream is being opened after the collection is read, so writes made " +
			"in between are invisible to both: the snapshot was taken before them and the " +
			"stream starts after them. No error is reported and no count looks wrong")
	}
}
