package cassandra

import (
	"context"
	"log"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// CassandraSource implements the hermod.Source interface for Cassandra CDC.
type CassandraSource struct {
	hosts  []string
	useCDC bool
}

func NewCassandraSource(hosts []string, useCDC bool) *CassandraSource {
	return &CassandraSource{
		hosts:  hosts,
		useCDC: useCDC,
	}
}

func (c *CassandraSource) Read(ctx context.Context) (hermod.Message, error) {
	if !c.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// TODO: Implement CDC by reading commit logs or using a CDC agent.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("cassandra-cdc-1")
		msg.SetOperation(hermod.OpCreate)
		msg.SetTable("events")
		msg.SetSchema("timeseries")
		msg.SetAfter([]byte(`{"event_id": "e1", "value": 1.23}`))
		msg.SetMetadata("source", "cassandra")
		return msg, nil
	}
}

func (c *CassandraSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (c *CassandraSource) Ping(ctx context.Context) error {
	return nil
}

func (c *CassandraSource) Close() error {
	log.Println("Closing CassandraSource")
	return nil
}
