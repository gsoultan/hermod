package scylladb

import (
	"context"
	"log"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// ScyllaDBSource implements the hermod.Source interface for ScyllaDB CDC.
type ScyllaDBSource struct {
	hosts  []string
	useCDC bool
}

func NewScyllaDBSource(hosts []string, useCDC bool) *ScyllaDBSource {
	return &ScyllaDBSource{
		hosts:  hosts,
		useCDC: useCDC,
	}
}

func (s *ScyllaDBSource) Read(ctx context.Context) (hermod.Message, error) {
	if !s.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// TODO: Implement CDC by querying CDC log tables.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("scylladb-cdc-1")
		msg.SetOperation(hermod.OpUpdate)
		msg.SetTable("metrics")
		msg.SetSchema("monitoring")
		msg.SetAfter([]byte(`{"host": "h1", "cpu": 45.5}`))
		msg.SetMetadata("source", "scylladb")
		return msg, nil
	}
}

func (s *ScyllaDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *ScyllaDBSource) Ping(ctx context.Context) error {
	return nil
}

func (s *ScyllaDBSource) Close() error {
	log.Println("Closing ScyllaDBSource")
	return nil
}
