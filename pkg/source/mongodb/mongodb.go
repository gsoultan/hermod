package mongodb

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	clients   = make(map[string]*mongo.Client)
	clientsMu sync.RWMutex
)

func GetClient(uri string) (*mongo.Client, error) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
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
	uri             string
	database        string
	collection      string
	useCDC          bool
	client          *mongo.Client
	stream          *mongo.ChangeStream
	mu              sync.Mutex
	lastResumeToken bson.Raw
	msgChan         chan hermod.Message
}

func NewMongoDBSource(uri, database, collection string, useCDC bool) *MongoDBSource {
	return &MongoDBSource{
		uri:        uri,
		database:   database,
		collection: collection,
		useCDC:     useCDC,
		msgChan:    make(chan hermod.Message, 1000),
	}
}

func (m *MongoDBSource) init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client != nil {
		if !m.useCDC {
			return nil
		}
		// Already initialized, check if stream is still valid
		if m.stream != nil && m.stream.ID() != 0 {
			return nil
		}
	}

	if m.client == nil {
		client, err := GetClient(m.uri)
		if err != nil {
			return fmt.Errorf("failed to connect to mongodb: %w", err)
		}
		m.client = client
	}

	if !m.useCDC {
		return nil
	}

	opts := options.ChangeStream()
	if len(m.lastResumeToken) > 0 {
		opts.SetResumeAfter(m.lastResumeToken)
	}

	var stream *mongo.ChangeStream
	var err error
	if m.collection != "" {
		stream, err = m.client.Database(m.database).Collection(m.collection).Watch(ctx, mongo.Pipeline{}, opts)
	} else if m.database != "" {
		stream, err = m.client.Database(m.database).Watch(ctx, mongo.Pipeline{}, opts)
	} else {
		stream, err = m.client.Watch(ctx, mongo.Pipeline{}, opts)
	}

	if err != nil {
		return fmt.Errorf("failed to start change stream: %w", err)
	}

	if m.stream != nil {
		m.stream.Close(ctx)
	}
	m.stream = stream
	return nil
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

		if stream.Next(ctx) {
			var event bson.M
			if err := stream.Decode(&event); err != nil {
				return nil, fmt.Errorf("failed to decode change stream event: %w", err)
			}

			// Store resume token for internal reconnect
			token := stream.ResumeToken()
			m.mu.Lock()
			m.lastResumeToken = token
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
	cursor, err := m.client.Database(m.database).Collection(collection).Find(ctx, bson.M{})
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
	token, err := hex.DecodeString(tokenHex)
	if err != nil {
		return nil
	}
	m.mu.Lock()
	m.lastResumeToken = bson.Raw(token)
	m.mu.Unlock()
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
		client, err = GetClient(m.uri)
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
		return fmt.Errorf("mongodb change streams require a replica set or sharded cluster. Current deployment is a standalone instance")
	}

	return nil
}

func (m *MongoDBSource) Ping(ctx context.Context) error {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()

	if client == nil {
		var err error
		client, err = GetClient(m.uri)
		if err != nil {
			return fmt.Errorf("failed to connect to mongodb for ping: %w", err)
		}
	}
	return client.Ping(ctx, nil)
}

func (m *MongoDBSource) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
	if len(m.lastResumeToken) == 0 {
		return nil
	}
	return map[string]string{
		"resume_token": hex.EncodeToString(m.lastResumeToken),
	}
}

func (m *MongoDBSource) SetState(state map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tokenHex, ok := state["resume_token"]; ok {
		if token, err := hex.DecodeString(tokenHex); err == nil {
			m.lastResumeToken = bson.Raw(token)
		}
	}
}

func (m *MongoDBSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	client, err := GetClient(m.uri)
	if err != nil {
		return nil, err
	}
	return client.ListDatabaseNames(ctx, bson.M{})
}

func (m *MongoDBSource) DiscoverTables(ctx context.Context) ([]string, error) {
	client, err := GetClient(m.uri)
	if err != nil {
		return nil, err
	}
	db := client.Database(m.database)
	return db.ListCollectionNames(ctx, bson.M{})
}

func (m *MongoDBSource) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	client, err := GetClient(m.uri)
	if err != nil {
		return nil, err
	}

	var doc bson.M
	err = client.Database(m.database).Collection(table).FindOne(ctx, bson.M{}).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
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
	client, err := GetClient(m.uri)
	if err != nil {
		return nil, err
	}

	targetColl := table
	if targetColl == "" {
		targetColl = m.collection
	}
	if targetColl == "" {
		return nil, fmt.Errorf("no collection specified for sampling")
	}

	coll := client.Database(m.database).Collection(targetColl)
	var result bson.M
	err = coll.FindOne(ctx, bson.M{}).Decode(&result)
	if err != nil {
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
