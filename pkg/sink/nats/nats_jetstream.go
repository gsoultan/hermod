package nats

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/user/hermod"
)

// NatsJetStreamSink implements the hermod.Sink interface for NATS JetStream.
type NatsJetStreamSink struct {
	nc        *nats.Conn
	js        nats.JetStreamContext
	subject   string
	formatter hermod.Formatter
}

// NewNatsJetStreamSink creates a new NATS JetStream sink.
func NewNatsJetStreamSink(url string, subject string, username, password, token string, formatter hermod.Formatter) (*NatsJetStreamSink, error) {
	opts := []nats.Option{}
	if token != "" {
		opts = append(opts, nats.Token(token))
	} else if username != "" {
		opts = append(opts, nats.UserInfo(username, password))
	}

	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	return &NatsJetStreamSink{
		nc:        nc,
		js:        js,
		subject:   subject,
		formatter: formatter,
	}, nil
}

// Write publishes a message to NATS JetStream.
func (s *NatsJetStreamSink) Write(ctx context.Context, msg hermod.Message) error {
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data, err = json.Marshal(map[string]interface{}{
			"id":        msg.ID(),
			"operation": msg.Operation(),
			"table":     msg.Table(),
			"schema":    msg.Schema(),
			"before":    json.RawMessage(msg.Before()),
			"after":     json.RawMessage(msg.After()),
			"metadata":  msg.Metadata(),
		})
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	_, err = s.js.Publish(s.subject, data, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("failed to publish message to JetStream: %w", err)
	}

	return nil
}

// Ping checks if the NATS connection is alive.
func (s *NatsJetStreamSink) Ping(ctx context.Context) error {
	if s.nc == nil {
		return fmt.Errorf("nats connection is nil")
	}
	if !s.nc.IsConnected() {
		return fmt.Errorf("nats is not connected")
	}
	return nil
}

// Close closes the NATS connection.
func (s *NatsJetStreamSink) Close() error {
	s.nc.Close()
	return nil
}
