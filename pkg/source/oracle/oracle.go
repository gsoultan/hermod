package oracle

import (
	"context"
	"log"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// OracleSource implements the hermod.Source interface for Oracle CDC.
type OracleSource struct {
	connString string
	useCDC     bool
}

func NewOracleSource(connString string, useCDC bool) *OracleSource {
	return &OracleSource{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (o *OracleSource) Read(ctx context.Context) (hermod.Message, error) {
	if !o.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
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
	log.Println("Closing OracleSource")
	return nil
}
