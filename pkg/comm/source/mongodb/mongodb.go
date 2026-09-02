package mongodb

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	hermod "github.com/gsoultan/Hermod"
	"github.com/gsoultan/Hermod/pkg/comm/message"
	sourcebuf "github.com/gsoultan/Hermod/pkg/comm/source"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	clients   = make(map[string]*mongo.Client)
	clientsMu sync.RWMutex
)

// GetClient returns the shared client for uri, dialling it on first use.
//
// It takes a context because the verification ping below has to be bounded by
// whatever the caller allowed. It previously built its own 10s context from
// Background and ignored the caller's entirely, so a source Read or a readiness
// Ping with a two-second budget still waited ten — and the driver's own
// server-selection timeout could stretch that further.
func GetClient(ctx context.Context, uri string) (*mongo.Client, error) {
	clientsMu.RLock()
	client, ok := clients[uri]
	clientsMu.RUnlock()
	if ok {
		return client, nil
	}

	clientsMu.Lock()
	defer clientsMu.Unlock()
	client, ok = clients[uri]
	if ok {
		return client, nil
	}

	// Cap the wait at 10s, but never exceed the caller's deadline.
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = dialCtx

	// Bound server selection too: without this the driver spends its own
	// default (30s) looking for a reachable node before the ping even runs.
	opts := options.Client().ApplyURI(uri)
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 {
			opts = opts.SetServerSelectionTimeout(remaining)
		}
	}

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}

	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx)
		return nil, err
	}

	clients[uri] = client
	return client, nil
}

// MongoDBSource implements the hermod.Source interface for MongoDB Change Streams.
type MongoDBSource struct {
	uri        string
	database   string
	collection string
	useCDC     bool
	client     *mongo.Client
	stream     *mongo.ChangeStream
	mu         sync.Mutex
	// lastResumeToken is the position of the last acknowledged message, and the
	// only one GetState reports. The engine persists that on every ack
	// (registry_routing.go, statefulSource.Ack), so it decides where a restart
	// comes back to: anything ahead of it is a message the pipeline will never
	// be given again.
	lastResumeToken bson.Raw
	// readToken is how far the stream has been read, which is further ahead
	// whenever messages are in flight. It is used to reopen the stream inside
	// this process, where those in-flight messages are still alive and will be
	// acknowledged, and it is deliberately not persisted.
	readToken bson.Raw
	msgChan   chan hermod.Message
	logger    hermod.Logger

	// initialLoad asks for the documents already in the collection to be
	// carried across before the change stream is tailed. Off by default:
	// turning it on for a workflow that is already running would re-read the
	// whole collection the next time it restarted.
	initialLoad bool
	// initialLoadComplete is the source's record of having backfilled, and the
	// only thing that stops it happening again. PostgreSQL needs no equivalent
	// because the replication slot is its own record; a change stream leaves
	// nothing behind, and the resume token cannot stand in — snapshot messages
	// carry none, so a collection that is backfilled and then never written to
	// would be indistinguishable from one that had never run.
	initialLoadComplete bool
	// initialLoadStarted guards the backfill to one run per process. init() is
	// called again whenever the stream has to be reopened, and without this a
	// stream error before the first change event would start it over.
	initialLoadStarted bool
	// initialLoadDone is closed when the backfill finishes. Until then Read
	// takes from the buffered snapshot rather than the stream, so the documents
	// that were already there arrive ahead of any change to them.
	initialLoadDone chan struct{}
	backfillCancel  context.CancelFunc
	// streamStart is the cluster time the change stream was opened at, and the
	// floor a restart falls back to when no message has been acknowledged yet.
	//
	// Without it there is a window with no persisted position at all: a
	// backfill records that it ran but moves no stream position, because
	// snapshot messages carry no resume token. A source that backfilled a quiet
	// collection and was then restarted would open a stream from *now* and
	// silently skip everything written while it was down. The resume token
	// takes over as soon as one real change is acknowledged; this only has to
	// cover the gap before that.
	streamStart *bson.Timestamp
}

// SetLogger installs the logger the engine propagates to sources that accept
// one. The backfill reports what it could not carry across through it.
func (m *MongoDBSource) SetLogger(logger hermod.Logger) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logger = logger
}

func (m *MongoDBSource) log(level, msg string, keysAndValues ...any) {
	m.mu.Lock()
	logger := m.logger
	m.mu.Unlock()

	if logger == nil {
		// Standard log rather than nothing: the only messages this source emits
		// are about documents it could not carry across, and those must not
		// vanish because no logger happened to be installed.
		if len(keysAndValues) > 0 {
			log.Printf("[%s] %s %v", level, msg, keysAndValues)
		} else {
			log.Printf("[%s] %s", level, msg)
		}
		return
	}

	switch level {
	case "DEBUG":
		logger.Debug(msg, keysAndValues...)
	case "INFO":
		logger.Info(msg, keysAndValues...)
	case "WARN":
		logger.Warn(msg, keysAndValues...)
	case "ERROR":
		logger.Error(msg, keysAndValues...)
	}
}

// SetInitialLoad asks for a one-time backfill of the watched collection before
// the change stream is tailed.
//
// It runs only when the source has no record of having run before, so enabling
// it on a workflow that is already streaming does nothing until that record is
// cleared. See initialLoadComplete for what the record is and why the resume
// token cannot serve as one.
func (m *MongoDBSource) SetInitialLoad(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.initialLoad = enabled
}

func NewMongoDBSource(uri, database, collection string, useCDC bool) *MongoDBSource {
	return &MongoDBSource{
		uri:        uri,
		database:   database,
		collection: collection,
		useCDC:     useCDC,
		msgChan:    make(chan hermod.Message, sourcebuf.DefaultSourceBuffer),
	}
}

func (m *MongoDBSource) init(ctx context.Context) error {
	m.mu.Lock()
	if m.client != nil {
		if !m.useCDC || (m.stream != nil && m.stream.ID() != 0) {
			m.mu.Unlock()
			return nil
		}
	}
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return fmt.Errorf("failed to connect to mongodb: %w", err)
		}
		m.mu.Lock()
		if m.client == nil {
			m.client = client
		} else {
			// Someone else initialized it
			_ = client.Disconnect(ctx)
			client = m.client
		}
		m.mu.Unlock()
	}

	if !m.useCDC {
		return nil
	}

	opts := options.ChangeStream()
	m.mu.Lock()
	resumeFrom := m.readToken
	if len(resumeFrom) == 0 {
		resumeFrom = m.lastResumeToken
	}
	startAt := m.streamStart
	wantBackfill := m.initialLoad && !m.initialLoadStarted && !m.initialLoadComplete && len(m.lastResumeToken) == 0
	m.mu.Unlock()

	switch {
	case len(resumeFrom) > 0:
		// A token is exact, so it always wins over a timestamp floor.
		opts.SetResumeAfter(resumeFrom)
	case startAt != nil:
		// No message has been acknowledged since the stream was first opened,
		// so the only position on record is where it began. Resuming there
		// replays whatever happened during the downtime; the overlap with the
		// backfill is duplicates, which sink-side idempotency collapses.
		opts.SetStartAtOperationTime(startAt)
	case wantBackfill:
		// About to read the collection. Pin the stream to the cluster time
		// *before* that starts, so the position survives a restart that
		// happens before any change is acknowledged. A failure here is not
		// fatal: the stream still opens at "now", which is what it would have
		// done anyway.
		if ts, err := clusterTime(ctx, client); err != nil {
			m.log("WARN", "Could not read the server's cluster time; the change stream will "+
				"start from now, so a restart before the first acknowledged change would "+
				"skip anything written in between", "error", err.Error())
		} else {
			opts.SetStartAtOperationTime(&ts)
			m.mu.Lock()
			m.streamStart = &ts
			m.mu.Unlock()
		}
	}

	var stream *mongo.ChangeStream
	var err error
	if m.collection != "" {
		stream, err = client.Database(m.database).Collection(m.collection).Watch(ctx, mongo.Pipeline{}, opts)
	} else if m.database != "" {
		stream, err = client.Database(m.database).Watch(ctx, mongo.Pipeline{}, opts)
	} else {
		stream, err = client.Watch(ctx, mongo.Pipeline{}, opts)
	}

	if err != nil {
		return fmt.Errorf("failed to start change stream: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream != nil {
		m.stream.Close(ctx)
	}
	m.stream = stream

	// The backfill starts only now, with the stream already open, and that
	// order is the whole point. A write that lands while the collection is
	// being read is behind the snapshot but ahead of the stream's starting
	// position, so the stream still reports it. Open the stream afterwards
	// instead and such a write falls between the two: too late for the
	// snapshot, too early for the tail. Nothing errors and no count looks
	// wrong — the document is simply not there.
	//
	// Where the two overlap the document arrives twice, which is the ordinary
	// at-least-once bargain: both paths key the message on the document id, so
	// sink-side idempotency collapses them.
	if m.initialLoad && !m.initialLoadStarted && !m.initialLoadComplete && len(m.lastResumeToken) == 0 {
		m.initialLoadStarted = true
		m.initialLoadDone = make(chan struct{})
		backfillCtx, cancel := context.WithCancel(context.Background())
		m.backfillCancel = cancel
		go m.runInitialLoad(backfillCtx, m.initialLoadDone)
	}
	return nil
}

// runInitialLoad carries across the documents that were already in the
// collection when the stream was opened.
//
// It cannot run inline in Read(): it delivers into the same channel Read
// drains, so the first call would block on a full buffer and never return to
// empty it. That is also why Read prefers the buffer to the stream until this
// finishes — see backfillPending.
//
// A failure is logged rather than returned. The stream is open and holding a
// position by this point, so refusing to serve it would strand a working tail
// to no purpose; what is lost is the pre-existing documents, which is what the
// log says. The completion record is not written in that case, so the next
// restart tries again rather than quietly settling for a partial copy.
func (m *MongoDBSource) runInitialLoad(ctx context.Context, done chan struct{}) {
	defer close(done)

	collections, err := m.initialLoadCollections(ctx)
	if err != nil {
		m.log("ERROR", "Initial load could not list collections; documents already in the "+
			"source will not be carried across", "error", err.Error())
		return
	}

	m.log("INFO", "Initial load starting", "collections", strings.Join(collections, ","))
	failed := 0
	for _, coll := range collections {
		if ctx.Err() != nil {
			return
		}
		if err := m.snapshotCollection(ctx, coll); err != nil {
			failed++
			m.log("ERROR", "Initial load failed for a collection; its existing documents were "+
				"not carried across, though later changes to it will still stream",
				"collection", coll, "error", err.Error())
		}
	}
	if failed > 0 {
		m.log("WARN", "Initial load finished with failures; it will run again on the next "+
			"restart because no completion was recorded", "collections_failed", failed)
		return
	}

	m.mu.Lock()
	m.initialLoadComplete = true
	m.mu.Unlock()
	m.log("INFO", "Initial load complete; streaming changes from the open change stream")
}

// clusterTime reports the server's current cluster time, which is the unit a
// change stream's startAtOperationTime is expressed in. Taken from hello
// because every deployment answers it and it needs no privileges beyond
// connecting.
func clusterTime(ctx context.Context, client *mongo.Client) (bson.Timestamp, error) {
	var reply struct {
		ClusterTime struct {
			ClusterTime bson.Timestamp `bson:"clusterTime"`
		} `bson:"$clusterTime"`
	}
	if err := client.Database("admin").
		RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).
		Decode(&reply); err != nil {
		return bson.Timestamp{}, err
	}
	if reply.ClusterTime.ClusterTime.T == 0 {
		return bson.Timestamp{}, errors.New("server reported no $clusterTime; it is not a replica set member")
	}
	return reply.ClusterTime.ClusterTime, nil
}

func (m *MongoDBSource) initialLoadCollections(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	collection := m.collection
	m.mu.Unlock()
	if collection != "" {
		return []string{collection}, nil
	}
	return m.DiscoverTables(ctx)
}

// backfillPending reports the channel to wait on while the initial load is
// still running, or nil once it has finished. Read consults it to take from the
// buffered snapshot rather than blocking on the stream, which would otherwise
// stall the backfill until some unrelated write happened to arrive — and hand
// that write to the pipeline ahead of documents the snapshot had already read.
func (m *MongoDBSource) backfillPending() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.initialLoadDone == nil {
		return nil
	}
	select {
	case <-m.initialLoadDone:
		return nil
	default:
		return m.initialLoadDone
	}
}

func (m *MongoDBSource) Read(ctx context.Context) (hermod.Message, error) {
	if !m.useCDC {
		if m.client == nil {
			if err := m.init(ctx); err != nil {
				return nil, err
			}
		}
		// If not CDC, we only return messages from msgChan (e.g. snapshots)
		select {
		case msg := <-m.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	for {
		// Check for manual snapshot messages first
		select {
		case msg := <-m.msgChan:
			return msg, nil
		default:
		}

		m.mu.Lock()
		stream := m.stream
		m.mu.Unlock()

		if stream == nil {
			if err := m.init(ctx); err != nil {
				return nil, err
			}
			m.mu.Lock()
			stream = m.stream
			m.mu.Unlock()
		}

		// While the initial load is running, wait on it rather than on the
		// stream. stream.Next blocks until something changes, which for a quiet
		// collection is indefinitely: the backfill would fill the buffer, stall,
		// and deliver nothing until an unrelated write arrived — and that write
		// would then be handed over ahead of documents the snapshot had already
		// read, which is the ordering the backfill exists to get right.
		if done := m.backfillPending(); done != nil {
			select {
			case msg := <-m.msgChan:
				return msg, nil
			case <-done:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}

		if stream.Next(ctx) {
			var event bson.M
			if err := stream.Decode(&event); err != nil {
				return nil, fmt.Errorf("failed to decode change stream event: %w", err)
			}

			// Only the read position moves here. Advancing the acknowledged
			// position on read is what lost data: the engine persists GetState
			// on every ack, so a message that had been read and not yet
			// delivered was already behind the saved position, and a restart
			// never handed it out again.
			token := stream.ResumeToken()
			m.mu.Lock()
			m.readToken = token
			m.mu.Unlock()

			msg := message.AcquireMessage()
			msg.SetMetadata("resume_token", hex.EncodeToString(token))

			// Extract ID
			if documentKey, ok := event["documentKey"].(bson.M); ok {
				if id, ok := documentKey["_id"]; ok {
					msg.SetID(fmt.Sprintf("%v", id))
				}
			}

			// Extract Operation
			opType, _ := event["operationType"].(string)
			switch opType {
			case "insert":
				msg.SetOperation(hermod.OpCreate)
			case "update", "replace":
				msg.SetOperation(hermod.OpUpdate)
			case "delete":
				msg.SetOperation(hermod.OpDelete)
			case "invalidate":
				m.mu.Lock()
				m.stream = nil
				m.mu.Unlock()
				continue
			default:
				msg.SetOperation(hermod.OpUpdate)
			}

			msg.SetTable(m.collection)
			msg.SetSchema(m.database)

			if fullDocument, ok := event["fullDocument"]; ok {
				afterBytes, _ := bson.MarshalExtJSON(fullDocument, true, true)
				msg.SetAfter(afterBytes)
			}

			if fullDocumentBefore, ok := event["fullDocumentBeforeChange"]; ok {
				beforeBytes, _ := bson.MarshalExtJSON(fullDocumentBefore, true, true)
				msg.SetBefore(beforeBytes)
			}

			msg.SetMetadata("source", "mongodb")
			msg.SetMetadata("operation_type", opType)
			if clusterTime, ok := event["clusterTime"].(time.Time); ok {
				msg.SetMetadata("cluster_time", clusterTime.Format(time.RFC3339))
			}

			return msg, nil
		}

		if err := stream.Err(); err != nil {
			m.mu.Lock()
			m.stream = nil
			m.mu.Unlock()
			return nil, fmt.Errorf("change stream error: %w", err)
		}

		m.mu.Lock()
		m.stream = nil
		m.mu.Unlock()

		select {
		case msg := <-m.msgChan:
			return msg, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			continue
		}
	}
}

func (m *MongoDBSource) Snapshot(ctx context.Context, tables ...string) error {
	if err := m.init(ctx); err != nil {
		return err
	}

	targetCollections := tables
	if len(targetCollections) == 0 {
		if m.collection != "" {
			targetCollections = []string{m.collection}
		} else {
			var err error
			targetCollections, err = m.DiscoverTables(ctx)
			if err != nil {
				return err
			}
		}
	}

	for _, collName := range targetCollections {
		if err := m.snapshotCollection(ctx, collName); err != nil {
			return err
		}
	}
	return nil
}

func (m *MongoDBSource) snapshotCollection(ctx context.Context, collection string) error {
	// Taken under the lock. This runs on the backfill goroutine now, and Close
	// clears the client from another one; reading the field directly was a race
	// that only became reachable when the snapshot stopped being synchronous.
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return errors.New("the source was closed before the collection could be read")
	}

	cursor, err := client.Database(m.database).Collection(collection).Find(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("failed to find documents in collection %q: %w", collection, err)
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var doc bson.M
		if err := cursor.Decode(&doc); err != nil {
			return fmt.Errorf("failed to decode document: %w", err)
		}

		msg := message.AcquireMessage()
		if id, ok := doc["_id"]; ok {
			msg.SetID(fmt.Sprintf("%v", id))
		} else {
			msg.SetID(fmt.Sprintf("snapshot-%s-%d", collection, time.Now().UnixNano()))
		}
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetTable(collection)
		msg.SetSchema(m.database)

		afterBytes, _ := bson.MarshalExtJSON(doc, true, true)
		msg.SetAfter(afterBytes)

		msg.SetMetadata("source", "mongodb")
		msg.SetMetadata("snapshot", "true")

		select {
		case m.msgChan <- msg:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return cursor.Err()
}

func (m *MongoDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	tokenHex := msg.Metadata()["resume_token"]
	if tokenHex == "" {
		return nil
	}
	// A malformed resume token is ignored: acking must still succeed, we just
	// keep the previously stored token.
	if token, err := hex.DecodeString(tokenHex); err == nil {
		m.mu.Lock()
		m.lastResumeToken = bson.Raw(token)
		m.mu.Unlock()
	}
	return nil
}

func (m *MongoDBSource) IsReady(ctx context.Context) error {
	if err := m.Ping(ctx); err != nil {
		return fmt.Errorf("mongodb connection failed: %w", err)
	}

	if !m.useCDC {
		return nil
	}

	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	var err error
	if client == nil {
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return fmt.Errorf("failed to connect to mongodb for readiness check: %w", err)
		}
	}

	// Check if it's a replica set or sharded cluster (required for Change Streams)
	var isMaster bson.M
	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}}).Decode(&isMaster)
	if err != nil {
		return fmt.Errorf("failed to run isMaster command: %w", err)
	}

	_, hasSetName := isMaster["setName"]
	msg, hasMsg := isMaster["msg"]
	isSharded := hasMsg && msg == "isdbgrid"

	if !hasSetName && !isSharded {
		return errors.New("mongodb change streams require a replica set or sharded cluster. Current deployment is a standalone instance")
	}

	return nil
}

func (m *MongoDBSource) Ping(ctx context.Context) error {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return fmt.Errorf("failed to connect to mongodb for ping: %w", err)
		}
	}
	return client.Ping(ctx, nil)
}

func (m *MongoDBSource) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop the backfill before the client goes: it reads through that client,
	// and a cursor left running against a disconnected one is a goroutine that
	// outlives the source.
	if m.backfillCancel != nil {
		m.backfillCancel()
		m.backfillCancel = nil
	}

	if m.stream != nil {
		m.stream.Close(context.Background())
		m.stream = nil
	}
	m.client = nil
	return nil
}

func (m *MongoDBSource) GetState() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.lastResumeToken) == 0 && !m.initialLoadComplete && m.streamStart == nil {
		return nil
	}
	state := make(map[string]string, 3)
	if len(m.lastResumeToken) > 0 {
		state["resume_token"] = hex.EncodeToString(m.lastResumeToken)
	}
	if m.streamStart != nil {
		state["stream_start_time"] = fmt.Sprintf("%d.%d", m.streamStart.T, m.streamStart.I)
	}
	// Reported separately from the resume token because it has to survive the
	// case where there is no token: acknowledging a backfill moves no stream
	// position, so without this a collection that was carried across and then
	// never written to would be backfilled again on every restart. The engine
	// persists this map after every ack, so the first ack that follows a
	// completed backfill writes the record down.
	if m.initialLoadComplete {
		state["initial_load"] = "done"
	}
	return state
}

func (m *MongoDBSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// readToken is cleared with it: the messages after this point were never
	// acknowledged, so this is where reading has to start again.
	m.readToken = nil
	if tokenHex, ok := state["resume_token"]; ok {
		if token, err := hex.DecodeString(tokenHex); err == nil {
			m.lastResumeToken = bson.Raw(token)
		}
	}
	if state["initial_load"] == "done" {
		m.initialLoadComplete = true
	}
	if raw, ok := state["stream_start_time"]; ok {
		var t, i uint32
		if _, err := fmt.Sscanf(raw, "%d.%d", &t, &i); err == nil && t > 0 {
			m.streamStart = &bson.Timestamp{T: t, I: i}
		}
	}
}

func (m *MongoDBSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Disconnect(ctx) }()
	}

	return client.ListDatabaseNames(ctx, bson.M{})
}

func (m *MongoDBSource) DiscoverTables(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Disconnect(ctx) }()
	}

	db := client.Database(m.database)
	return db.ListCollectionNames(ctx, bson.M{})
}

func (m *MongoDBSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Disconnect(ctx) }()
	}

	var doc bson.M
	err := client.Database(m.database).Collection(table).FindOne(ctx, bson.M{}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return []hermod.ColumnInfo{}, nil
		}
		return nil, err
	}

	var columns []hermod.ColumnInfo
	for k, v := range doc {
		columns = append(columns, hermod.ColumnInfo{
			Name:       k,
			Type:       fmt.Sprintf("%T", v),
			IsPK:       k == "_id",
			IsIdentity: k == "_id",
			IsNullable: k != "_id",
		})
	}
	return columns, nil
}

func (m *MongoDBSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(ctx, m.uri)
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Disconnect(ctx) }()
	}

	targetColl := table
	if targetColl == "" {
		targetColl = m.collection
	}
	if targetColl == "" {
		return nil, errors.New("no collection specified for sampling")
	}

	coll := client.Database(m.database).Collection(targetColl)
	var result bson.M
	if err := coll.FindOne(ctx, bson.M{}).Decode(&result); err != nil {
		return nil, err
	}

	afterJSON, _ := bson.MarshalExtJSON(result, true, true)

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s-%d", targetColl, time.Now().Unix()))
	msg.SetOperation(hermod.OpSnapshot)
	msg.SetTable(targetColl)
	msg.SetAfter(afterJSON)
	msg.SetMetadata("source", "mongodb")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
