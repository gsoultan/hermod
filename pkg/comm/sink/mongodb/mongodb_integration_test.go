//go:build integration

package mongodb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
)

// ---------------------------------------------------------------------------
// MongoDB sink, against a real server.
//
// The connector is listed Beta: substantial and unit-tested, but never run
// against live infrastructure, which is the line this project draws for GA. The
// distinction is not bureaucratic — the conformance suite proves a sink refuses
// a nil message and closes cleanly; it cannot prove a document actually lands,
// or that an update replaces rather than duplicates.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 MONGODB_URI=mongodb://127.0.0.1:27017 \
//	go test -tags=integration ./pkg/comm/sink/mongodb/
// ---------------------------------------------------------------------------

type mongoFixture struct {
	uri        string
	database   string
	collection string
	client     *mongo.Client
	sink       *MongoDBSink
}

func newMongoFixture(t *testing.T) *mongoFixture {
	t.Helper()

	uri := os.Getenv("MONGODB_URI")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || uri == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MONGODB_URI to run")
	}

	ctx := t.Context()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("MONGODB_URI names a server that is not reachable (%s): %v", uri, err)
	}
	// A failure, not a skip: naming the server in the environment says it should
	// be there, and skipping a suite whose infrastructure vanished is how a CI
	// job reports success having tested nothing.
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("MONGODB_URI names a server that is not reachable (%s): %v", uri, err)
	}

	db := os.Getenv("MONGODB_DB")
	if db == "" {
		db = "hermod_it"
	}
	// Named for the test, so a leftover collection is traceable to what made it.
	coll := "sink_" + fmt.Sprintf("%x", time.Now().UnixNano())

	f := &mongoFixture{uri: uri, database: db, collection: coll, client: client}
	t.Cleanup(func() {
		_ = client.Database(db).Collection(coll).Drop(context.Background())
		if f.sink != nil {
			_ = f.sink.Close()
		}
		_ = client.Disconnect(context.Background())
	})
	return f
}

func (f *mongoFixture) newSink(operationMode string) *MongoDBSink {
	f.sink = NewMongoDBSink(f.uri, f.database, f.collection, nil, "", "", "", operationMode)
	return f.sink
}

func (f *mongoFixture) count(t *testing.T, filter bson.M) int64 {
	t.Helper()
	n, err := f.client.Database(f.database).Collection(f.collection).CountDocuments(t.Context(), filter)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func mongoMessage(t *testing.T, id string, op hermod.Operation, doc map[string]any) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetID(id)
	m.SetOperation(op)
	m.SetTable("ignored")
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	m.SetAfter(body)
	for k, v := range doc {
		m.SetData(k, v)
	}
	t.Cleanup(func() { message.ReleaseMessage(m) })
	return m
}

// TestADocumentActuallyLands is the evidence GA asks for: not that the sink
// accepted the write, but that the row is in the database afterwards.
func TestADocumentActuallyLands(t *testing.T) {
	f := newMongoFixture(t)
	sink := f.newSink("")

	msg := mongoMessage(t, "doc-1", hermod.OpCreate, map[string]any{"id": "doc-1", "name": "Ada"})
	if err := sink.Write(t.Context(), msg); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := f.count(t, bson.M{"name": "Ada"}); got != 1 {
		t.Errorf("the collection holds %d matching documents, want 1; the write was "+
			"accepted but nothing landed", got)
	}
}

// TestTheSameKeyTwiceDoesNotDuplicate. A CDC pipeline replays on restart, and
// at-least-once delivery means the same row arrives more than once. A sink that
// inserts blindly turns every replay into duplicate data.
func TestTheSameKeyTwiceDoesNotDuplicate(t *testing.T) {
	f := newMongoFixture(t)
	sink := f.newSink("")

	for range 2 {
		msg := mongoMessage(t, "doc-2", hermod.OpCreate, map[string]any{"id": "doc-2", "name": "Grace"})
		if err := sink.Write(t.Context(), msg); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	if got := f.count(t, bson.M{"id": "doc-2"}); got != 1 {
		t.Errorf("the same key written twice produced %d documents; a replay after a "+
			"restart would duplicate every row", got)
	}
}

// TestAnUpdateReplacesTheDocument rather than adding another one.
func TestAnUpdateReplacesTheDocument(t *testing.T) {
	f := newMongoFixture(t)
	sink := f.newSink("")

	create := mongoMessage(t, "doc-3", hermod.OpCreate, map[string]any{"id": "doc-3", "name": "Ada"})
	if err := sink.Write(t.Context(), create); err != nil {
		t.Fatalf("create: %v", err)
	}
	update := mongoMessage(t, "doc-3", hermod.OpUpdate, map[string]any{"id": "doc-3", "name": "Ada Lovelace"})
	if err := sink.Write(t.Context(), update); err != nil {
		t.Fatalf("update: %v", err)
	}

	if got := f.count(t, bson.M{"id": "doc-3"}); got != 1 {
		t.Errorf("after an update the key has %d documents, want 1", got)
	}
	if got := f.count(t, bson.M{"name": "Ada Lovelace"}); got != 1 {
		t.Errorf("the updated value is not in the database; the update did not apply")
	}
}

// TestADeleteRemovesTheDocument. A CDC pipeline that cannot express a delete
// leaves the target diverging from the source forever.
func TestADeleteRemovesTheDocument(t *testing.T) {
	f := newMongoFixture(t)
	sink := f.newSink("")

	create := mongoMessage(t, "doc-4", hermod.OpCreate, map[string]any{"id": "doc-4", "name": "Alan"})
	if err := sink.Write(t.Context(), create); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := f.count(t, bson.M{"id": "doc-4"}); got != 1 {
		t.Fatalf("the document was never created, so this proves nothing about deletes")
	}

	del := mongoMessage(t, "doc-4", hermod.OpDelete, map[string]any{"id": "doc-4"})
	if err := sink.Write(t.Context(), del); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if got := f.count(t, bson.M{"id": "doc-4"}); got != 0 {
		t.Errorf("the document survived a delete (%d remaining); the target would diverge "+
			"from the source permanently", got)
	}
}

// TestABatchLandsWhole. Batching is where a sink is most likely to drop the
// tail of a slice or stop at the first element.
func TestABatchLandsWhole(t *testing.T) {
	f := newMongoFixture(t)
	sink := f.newSink("")

	var batch []hermod.Message
	for i := range 25 {
		batch = append(batch, mongoMessage(t, fmt.Sprintf("b-%d", i), hermod.OpCreate,
			map[string]any{"id": fmt.Sprintf("b-%d", i), "n": i, "batch": true}))
	}
	if err := sink.WriteBatch(t.Context(), batch); err != nil {
		t.Fatalf("write batch: %v", err)
	}

	if got := f.count(t, bson.M{"batch": true}); got != 25 {
		t.Errorf("a batch of 25 landed %d documents; the rest were accepted and lost", got)
	}
}
