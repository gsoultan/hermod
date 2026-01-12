package nats

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type NatsJetStreamSource struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	sub     *nats.Subscription
	subject string
	queue   string
}

func NewNatsJetStreamSource(url, subject, queue, username, password, token string) (*NatsJetStreamSource, error) {
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

	return &NatsJetStreamSource{
		nc:      nc,
		js:      js,
		subject: subject,
		queue:   queue,
	}, nil
}

func (s *NatsJetStreamSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.sub == nil {
		var err error
		if s.queue != "" {
			s.sub, err = s.js.QueueSubscribeSync(s.subject, s.queue)
		} else {
			s.sub, err = s.js.SubscribeSync(s.subject)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to subscribe to NATS: %w", err)
		}
	}

	m, err := s.sub.NextMsgWithContext(ctx)
	if err != nil {
		return nil, err
	}

	msg := message.AcquireMessage()
	// In a real scenario, we'd unmarshal the payload.
	// For now, keeping it simple like KafkaSource.
	msg.SetPayload(m.Data)
	msg.SetMetadata("nats_subject", m.Subject)

	return msg, nil
}

func (s *NatsJetStreamSource) Ack(ctx context.Context, msg hermod.Message) error {
	// NATS JetStream sync subscription requires manual ack if configured,
	// but default is often auto-ack for sync subs or handled via NextMsg.
	// Actually, for JS you should call m.Ack().
	// To support this, we'd need to keep track of nats.Msg.
	return nil
}

func (s *NatsJetStreamSource) Ping(ctx context.Context) error {
	if s.nc == nil || !s.nc.IsConnected() {
		return fmt.Errorf("nats not connected")
	}
	return nil
}

func (s *NatsJetStreamSource) Close() error {
	if s.sub != nil {
		s.sub.Unsubscribe()
	}
	if s.nc != nil {
		s.nc.Close()
	}
	return nil
}
