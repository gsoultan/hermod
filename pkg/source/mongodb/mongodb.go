package mongodb

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// MongoDBSource implements the hermod.Source interface for MongoDB Change Streams.
type MongoDBSource struct {
	uri string
}

func NewMongoDBSource(uri string) *MongoDBSource {
	return &MongoDBSource{
		uri: uri,
	}
}

func (m *MongoDBSource) Read(ctx context.Context) (hermod.Message, error) {
	// TODO: Implement Change Streams using mongo-go-driver.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("mongo-cdc-1")
		msg.SetOperation(hermod.OpCreate)
		msg.SetTable("products")
		msg.SetSchema("catalog")
		msg.SetAfter([]byte(`{"_id": "p1", "name": "laptop"}`))
		msg.SetMetadata("source", "mongodb")
		return msg, nil
	}
}

func (m *MongoDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (m *MongoDBSource) Ping(ctx context.Context) error {
	return nil
}

func (m *MongoDBSource) Close() error {
	fmt.Println("Closing MongoDBSource")
	return nil
}
