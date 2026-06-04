package cdc

import (
	"context"
	"errors"

	"github.com/user/hermod"
)

// OracleConnector implements CDC for Oracle databases using LogMiner.
type OracleConnector struct{}

func NewOracleConnector() *OracleConnector {
	return &OracleConnector{}
}

func (c *OracleConnector) Read(ctx context.Context) (hermod.Message, error) {
	return nil, errors.New("not implemented")
}

func (c *OracleConnector) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (c *OracleConnector) Ping(ctx context.Context) error {
	return nil
}

func (c *OracleConnector) Close() error {
	return nil
}

func (c *OracleConnector) Stream(ctx context.Context, checkpoint string) error {
	return errors.New("not implemented")
}
