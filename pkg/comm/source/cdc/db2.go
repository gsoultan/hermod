package cdc

import (
	"context"
	"errors"

	"github.com/user/hermod"
)

// DB2Connector implements CDC for IBM DB2 databases.
type DB2Connector struct{}

func NewDB2Connector() *DB2Connector {
	return &DB2Connector{}
}

func (c *DB2Connector) Read(ctx context.Context) (hermod.Message, error) {
	return nil, errors.New("not implemented")
}

func (c *DB2Connector) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (c *DB2Connector) Ping(ctx context.Context) error {
	return nil
}

func (c *DB2Connector) Close() error {
	return nil
}

func (c *DB2Connector) Stream(ctx context.Context, checkpoint string) error {
	return errors.New("not implemented")
}
