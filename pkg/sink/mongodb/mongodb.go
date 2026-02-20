package mongodb

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/evaluator"
	"github.com/user/hermod/pkg/sqlutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoDBSink struct {
	uri              string
	database         string
	client           *mongo.Client
	tableName        string
	mappings         []sqlutil.ColumnMapping
	deleteStrategy   string
	softDeleteColumn string
	softDeleteValue  string
	operationMode    string
}

func NewMongoDBSink(uri string, database string, tableName string, mappings []sqlutil.ColumnMapping, deleteStrategy string, softDeleteColumn string, softDeleteValue string, operationMode string) *MongoDBSink {
	if operationMode == "" {
		operationMode = "auto"
	}
	return &MongoDBSink{
		uri:              uri,
		database:         database,
		tableName:        tableName,
		mappings:         mappings,
		deleteStrategy:   deleteStrategy,
		softDeleteColumn: softDeleteColumn,
		softDeleteValue:  softDeleteValue,
		operationMode:    operationMode,
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

	// Group messages by collection
	byCollection := make(map[string][]mongo.WriteModel)
	for _, msg := range msgs {
		op := msg.Operation()
		if s.operationMode != "auto" && s.operationMode != "" {
			switch s.operationMode {
			case "insert", "upsert", "update":
				op = hermod.OpCreate
			case "delete":
				op = hermod.OpDelete
			}
		}

		if op == "" {
			op = hermod.OpCreate
		}

		collName := s.tableName
		if collName == "" {
			collName = msg.Table()
		}

		var model mongo.WriteModel
		switch op {
		case hermod.OpCreate, hermod.OpSnapshot, hermod.OpUpdate:
			var data bson.M
			if len(s.mappings) > 0 {
				data = bson.M{}
				for _, m := range s.mappings {
					val := evaluator.GetMsgValByPath(msg, m.SourceField)
					targetKey := m.TargetColumn
					if m.IsPrimaryKey {
						targetKey = "_id"
					}
					// Handle Identity: if marked as identity and value is empty, skip it to let MongoDB generate _id if it's the PK
					if m.IsIdentity && (val == nil || val == "") {
						continue
					}
					data[targetKey] = val
				}
			} else {
				if err := bson.UnmarshalExtJSON(msg.Payload(), true, &data); err != nil {
					data = bson.M{"data": msg.Payload()}
				}
			}

			// Determine ID for filter
			id := msg.ID()
			if len(s.mappings) > 0 {
				for _, m := range s.mappings {
					if m.IsPrimaryKey {
						if val, ok := data["_id"]; ok && val != nil && val != "" {
							id = fmt.Sprintf("%v", val)
						}
						break
					}
				}
			}

			model = mongo.NewUpdateOneModel().
				SetFilter(bson.M{"_id": id}).
				SetUpdate(bson.M{"$set": data}).
				SetUpsert(true)
		case hermod.OpDelete:
			if s.deleteStrategy == "ignore" {
				continue
			}
			id := msg.ID()
			if len(s.mappings) > 0 {
				for _, m := range s.mappings {
					if m.IsPrimaryKey {
						val := evaluator.GetMsgValByPath(msg, m.SourceField)
						if val != nil && val != "" {
							id = fmt.Sprintf("%v", val)
						}
						break
					}
				}
			}

			if s.deleteStrategy == "soft_delete" && s.softDeleteColumn != "" {
				model = mongo.NewUpdateOneModel().
					SetFilter(bson.M{"_id": id}).
					SetUpdate(bson.M{"$set": bson.M{s.softDeleteColumn: s.softDeleteValue}}).
					SetUpsert(true)
			} else {
				model = mongo.NewDeleteOneModel().
					SetFilter(bson.M{"_id": id})
			}
		default:
			return fmt.Errorf("unsupported operation: %s", op)
		}
		byCollection[collName] = append(byCollection[collName], model)
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
	client, err := mongo.Connect(options.Client().ApplyURI(s.uri))
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

func (s *MongoDBSink) DiscoverColumns(ctx context.Context, table string) ([]hermod.ColumnInfo, error) {
	if s.client == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
	}

	var doc bson.M
	err := s.client.Database(s.database).Collection(table).FindOne(ctx, bson.M{}).Decode(&doc)
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
