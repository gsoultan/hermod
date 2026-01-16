package mongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MongoDBSource implements the hermod.Source interface for MongoDB Change Streams.
type MongoDBSource struct {
	uri        string
	database   string
	collection string
	client     *mongo.Client
	stream     *mongo.ChangeStream
}

func NewMongoDBSource(uri, database, collection string) *MongoDBSource {
	return &MongoDBSource{
		uri:        uri,
		database:   database,
		collection: collection,
	}
}

func (m *MongoDBSource) init(ctx context.Context) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(m.uri))
	if err != nil {
		return fmt.Errorf("failed to connect to mongodb: %w", err)
	}

	m.client = client

	coll := client.Database(m.database).Collection(m.collection)
	stream, err := coll.Watch(ctx, mongo.Pipeline{})
	if err != nil {
		return fmt.Errorf("failed to start change stream: %w", err)
	}

	m.stream = stream
	return nil
}

func (m *MongoDBSource) Read(ctx context.Context) (hermod.Message, error) {
	if m.stream == nil {
		if err := m.init(ctx); err != nil {
			return nil, err
		}
	}

	for {
		if m.stream.Next(ctx) {
			var event bson.M
			if err := m.stream.Decode(&event); err != nil {
				return nil, fmt.Errorf("failed to decode change stream event: %w", err)
			}

			msg := message.AcquireMessage()

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
			default:
				msg.SetOperation(hermod.OpUpdate)
			}

			msg.SetTable(m.collection)
			msg.SetSchema(m.database)

			if fullDocument, ok := event["fullDocument"]; ok {
				afterBytes, _ := bson.MarshalExtJSON(fullDocument, true, true)
				msg.SetAfter(afterBytes)
			}

			msg.SetMetadata("source", "mongodb")
			if clusterTime, ok := event["clusterTime"].(time.Time); ok {
				msg.SetMetadata("cluster_time", clusterTime.Format(time.RFC3339))
			}

			return msg, nil
		}

		if err := m.stream.Err(); err != nil {
			return nil, fmt.Errorf("change stream error: %w", err)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			// Small sleep if no event and no error to avoid tight loop
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func (m *MongoDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	// MongoDB Change Streams are resume-token based.
	// To be truly production ready, we should store and use the resume token.
	return nil
}

func (m *MongoDBSource) Ping(ctx context.Context) error {
	if m.client == nil {
		return m.init(ctx)
	}
	return m.client.Ping(ctx, nil)
}

func (m *MongoDBSource) Close() error {
	if m.stream != nil {
		m.stream.Close(context.Background())
	}
	if m.client != nil {
		return m.client.Disconnect(context.Background())
	}
	return nil
}
