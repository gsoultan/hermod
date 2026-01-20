package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/user/hermod"
)

// NatsJetStreamSink implements the hermod.Sink interface for NATS JetStream.
type NatsJetStreamSink struct {
	nc        *nats.Conn
	js        nats.JetStreamContext
	url       string
	opts      []nats.Option
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

	return &NatsJetStreamSink{
		url:       url,
		opts:      opts,
		subject:   subject,
		formatter: formatter,
	}, nil
}

func (s *NatsJetStreamSink) ensureConnected(ctx context.Context) error {
	if s.nc != nil && s.nc.IsConnected() {
		return nil
	}

	nc, err := nats.Connect(s.url, s.opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return fmt.Errorf("failed to create JetStream context: %w", err)
	}

	s.nc = nc
	s.js = js
	return nil
}

// Write publishes a message to NATS JetStream.
func (s *NatsJetStreamSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		// Fallback to Payload-only if no formatter provided
		data = msg.Payload()
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
	return s.ensureConnected(ctx)
}

// Close closes the NATS connection.
func (s *NatsJetStreamSink) Close() error {
	if s.nc != nil {
		s.nc.Close()
	}
	return nil
}
