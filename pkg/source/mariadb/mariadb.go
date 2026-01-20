package mariadb

import (
	"context"
	"log"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// MariaDBSource implements the hermod.Source interface for MariaDB CDC.
type MariaDBSource struct {
	connString string
	useCDC     bool
}

func NewMariaDBSource(connString string, useCDC bool) *MariaDBSource {
	return &MariaDBSource{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (m *MariaDBSource) Read(ctx context.Context) (hermod.Message, error) {
	if !m.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// TODO: Implement binlog replication.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("mariadb-cdc-1")
		msg.SetOperation(hermod.OpDelete)
		msg.SetTable("sessions")
		msg.SetSchema("auth")
		msg.SetBefore([]byte(`{"session_id": "abc"}`))
		msg.SetMetadata("source", "mariadb")
		return msg, nil
	}
}

func (m *MariaDBSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (m *MariaDBSource) Ping(ctx context.Context) error {
	return nil
}

func (m *MariaDBSource) Close() error {
	log.Println("Closing MariaDBSource")
	return nil
}
