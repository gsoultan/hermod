package db2

import (
	"context"
	"log"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// DB2Source implements the hermod.Source interface for DB2 CDC.
type DB2Source struct {
	connString string
	useCDC     bool
}

func NewDB2Source(connString string, useCDC bool) *DB2Source {
	return &DB2Source{
		connString: connString,
		useCDC:     useCDC,
	}
}

func (d *DB2Source) Read(ctx context.Context) (hermod.Message, error) {
	if !d.useCDC {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	// TODO: Implement CDC using DB2 read log API or polling.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		msg := message.AcquireMessage()
		msg.SetID("db2-cdc-1")
		msg.SetOperation(hermod.OpUpdate)
		msg.SetTable("inventory")
		msg.SetSchema("db2inst1")
		msg.SetAfter([]byte(`{"id": 100, "quantity": 50}`))
		msg.SetMetadata("source", "db2")
		return msg, nil
	}
}

func (d *DB2Source) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (d *DB2Source) Ping(ctx context.Context) error {
	return nil
}

func (d *DB2Source) Close() error {
	log.Println("Closing DB2Source")
	return nil
}
