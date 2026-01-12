package oracle

import (
	"context"
	"fmt"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// OracleSource implements the hermod.Source interface for Oracle CDC.
type OracleSource struct {
	connString string
}

func NewOracleSource(connString string) *OracleSource {
	return &OracleSource{
		connString: connString,
	}
}

func (o *OracleSource) Read(ctx context.Context) (hermod.Message, error) {
	// TODO: Implement CDC polling or LogMiner integration.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("oracle-cdc-1")
		msg.SetOperation(hermod.OpCreate)
		msg.SetTable("employees")
		msg.SetSchema("hr")
		msg.SetAfter([]byte(`{"id": 1, "name": "John Doe"}`))
		msg.SetMetadata("source", "oracle")
		return msg, nil
	}
}

func (o *OracleSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (o *OracleSource) Ping(ctx context.Context) error {
	return nil
}

func (o *OracleSource) Close() error {
	fmt.Println("Closing OracleSource")
	return nil
}
