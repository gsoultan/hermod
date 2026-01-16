package mongodb

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDBSink struct {
	uri      string
	database string
	client   *mongo.Client
}

func NewMongoDBSink(uri, database string) *MongoDBSink {
	return &MongoDBSink{
		uri:      uri,
		database: database,
	}
}

func (s *MongoDBSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	collection := s.client.Database(s.database).Collection(msg.Table())

	op := msg.Operation()
	if op == "" {
		op = hermod.OpCreate
	}

	switch op {
	case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
		var data bson.M
		// Try to unmarshal from JSON first as it's common in Hermod
		if err := bson.UnmarshalExtJSON(msg.Payload(), true, &data); err != nil {
			// Fallback to raw bytes if not valid JSON
			data = bson.M{"data": msg.Payload()}
		}

		filter := bson.M{"_id": msg.ID()}
		update := bson.M{"$set": data}
		opts := options.Update().SetUpsert(true)

		_, err := collection.UpdateOne(ctx, filter, update, opts)
		if err != nil {
			return fmt.Errorf("failed to write to mongodb: %w", err)
		}
	case hermod.OpDelete:
		filter := bson.M{"_id": msg.ID()}
		_, err := collection.DeleteOne(ctx, filter)
		if err != nil {
			return fmt.Errorf("failed to delete from mongodb: %w", err)
		}
	default:
		return fmt.Errorf("unsupported operation: %s", op)
	}

	return nil
}

func (s *MongoDBSink) init(ctx context.Context) error {
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(s.uri))
	if err != nil {
		return fmt.Errorf("failed to connect to mongodb: %w", err)
	}
	s.client = client
	return s.client.Ping(ctx, nil)
}

func (s *MongoDBSink) Ping(ctx context.Context) error {
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}
	return s.client.Ping(ctx, nil)
}

func (s *MongoDBSink) Close() error {
	if s.client != nil {
		return s.client.Disconnect(context.Background())
	}
	return nil
}
