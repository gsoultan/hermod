package yugabyte

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// YugabyteSource implements the hermod.Source interface for YugabyteDB CDC.
type YugabyteSource struct {
	connString string
}

func NewYugabyteSource(connString string) *YugabyteSource {
	return &YugabyteSource{
		connString: connString,
	}
}

func (y *YugabyteSource) Read(ctx context.Context) (hermod.Message, error) {
	// TODO: Implement CDC using Yugabyte's CDC service.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("yugabyte-cdc-1")
		msg.SetOperation(hermod.OpCreate)
		msg.SetTable("accounts")
		msg.SetSchema("public")
		msg.SetAfter([]byte(`{"id": "a1", "balance": 1000}`))
		msg.SetMetadata("source", "yugabyte")
		return msg, nil
	}
}

func (y *YugabyteSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (y *YugabyteSource) Ping(ctx context.Context) error {
	return nil
}

func (y *YugabyteSource) Close() error {
	fmt.Println("Closing YugabyteSource")
	return nil
}
