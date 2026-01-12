package clickhouse

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// ClickHouseSource implements the hermod.Source interface for ClickHouse CDC.
type ClickHouseSource struct {
	connString string
}

func NewClickHouseSource(connString string) *ClickHouseSource {
	return &ClickHouseSource{
		connString: connString,
	}
}

func (c *ClickHouseSource) Read(ctx context.Context) (hermod.Message, error) {
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
	fmt.Println("Closing ClickHouseSource")
	return nil
}
