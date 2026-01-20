package clickhouse

import (
	"context"
	"log"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// ClickHouseSource implements the hermod.Source interface for ClickHouse CDC.
type ClickHouseSource struct {
	connString string
	useCDC     bool
}

func NewClickHouseSource(connString string, useCDC bool) *ClickHouseSource {
	return &ClickHouseSource{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (c *ClickHouseSource) Read(ctx context.Context) (hermod.Message, error) {
	if !c.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// TODO: Implement CDC using ClickHouse's MySQL/PostgreSQL replication or polling.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("clickhouse-cdc-1")
		msg.SetOperation(hermod.OpCreate)
		msg.SetTable("logs")
		msg.SetSchema("default")
		msg.SetAfter([]byte(`{"id": 1, "message": "log entry"}`))
		msg.SetMetadata("source", "clickhouse")
		return msg, nil
	}
}

func (c *ClickHouseSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (c *ClickHouseSource) Ping(ctx context.Context) error {
	return nil
}

func (c *ClickHouseSource) Close() error {
	log.Println("Closing ClickHouseSource")
	return nil
}
