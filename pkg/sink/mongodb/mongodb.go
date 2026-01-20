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
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

func (s *MongoDBSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return err
		}
	}

	// Group messages by collection (table)
	byCollection := make(map[string][]mongo.WriteModel)
	for _, msg := range msgs {
		op := msg.Operation()
		if op == "" {
			op = hermod.OpCreate
		}

		var model mongo.WriteModel
		switch op {
		case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
			var data bson.M
			if err := bson.UnmarshalExtJSON(msg.Payload(), true, &data); err != nil {
				data = bson.M{"data": msg.Payload()}
			}
			model = mongo.NewUpdateOneModel().
				SetFilter(bson.M{"_id": msg.ID()}).
				SetUpdate(bson.M{"$set": data}).
				SetUpsert(true)
		case hermod.OpDelete:
			model = mongo.NewDeleteOneModel().
				SetFilter(bson.M{"_id": msg.ID()})
		default:
			return fmt.Errorf("unsupported operation: %s", op)
		}
		byCollection[msg.Table()] = append(byCollection[msg.Table()], model)
	}

	for collName, models := range byCollection {
		collection := s.client.Database(s.database).Collection(collName)
		_, err := collection.BulkWrite(ctx, models)
		if err != nil {
			return fmt.Errorf("failed to bulk write to mongodb collection %s: %w", collName, err)
		}
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

func (s *MongoDBSink) DiscoverDatabases(ctx context.Context) ([]string, error) {
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	return s.client.ListDatabaseNames(ctx, bson.M{})
}

func (s *MongoDBSink) DiscoverTables(ctx context.Context) ([]string, error) {
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}
	db := s.client.Database(s.database)
	return db.ListCollectionNames(ctx, bson.M{})
}
