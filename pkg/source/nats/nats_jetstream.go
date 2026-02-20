package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

type NatsJetStreamSource struct {
	nc          *nats.Conn
	js          nats.JetStreamContext
	sub         *nats.Subscription
	url         string
	opts        []nats.Option
	subject     string
	queue       string
	durable     string
	unackedMsgs map[string]*nats.Msg
	mu          sync.Mutex
}

func NewNatsJetStreamSource(url, subject, queue, durable, username, password, token string) (*NatsJetStreamSource, error) {
	opts := []nats.Option{}
	if token != "" {
		opts = append(opts, nats.Token(token))
	} else if username != "" {
		opts = append(opts, nats.UserInfo(username, password))
	}

	return &NatsJetStreamSource{
		url:         url,
		opts:        opts,
		subject:     subject,
		queue:       queue,
		durable:     durable,
		unackedMsgs: make(map[string]*nats.Msg),
	}, nil
}

func (s *NatsJetStreamSource) ensureConnected() error {
	s.mu.Lock()
	defer s.mu.Unlock()

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
	s.sub = nil
	return nil
}

func (s *NatsJetStreamSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.sub == nil {
		var err error
		var subOpts []nats.SubOpt
		subOpts = append(subOpts, nats.ManualAck())
		if s.durable != "" {
			subOpts = append(subOpts, nats.Durable(s.durable))
		}

		if s.queue != "" {
			s.sub, err = s.js.QueueSubscribeSync(s.subject, s.queue, subOpts...)
		} else {
			s.sub, err = s.js.SubscribeSync(s.subject, subOpts...)
		}
		if err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("failed to subscribe to NATS: %w", err)
		}
	}
	sub := s.sub
	s.mu.Unlock()

	m, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		if err == nats.ErrTimeout {
			return nil, nil // Return nil msg on timeout to allow loop to continue
		}
		return nil, err
	}

	msg := message.AcquireMessage()
	// Use NATS sequence as ID if possible, otherwise generate one
	msgID := m.Subject // Subject is not unique, but just for example
	// Better: use metadata or some unique property
	metadata, _ := m.Metadata()
	if metadata != nil {
		msgID = fmt.Sprintf("%d", metadata.Sequence.Stream)
	}
	if msgID == "" {
		msgID = m.Subject + "_" + fmt.Sprintf("%x", m.Data)
	}

	msg.SetID(msgID)
	msg.SetPayload(m.Data)

	// Try to unmarshal JSON into Data() for dynamic structure
	var jsonData map[string]any
	if err := json.Unmarshal(m.Data, &jsonData); err == nil {
		for k, v := range jsonData {
			msg.SetData(k, v)
		}
	} else {
		msg.SetAfter(m.Data) // Fallback for non-JSON
	}

	msg.SetMetadata("nats_subject", m.Subject)

	s.mu.Lock()
	s.unackedMsgs[msg.ID()] = m
	s.mu.Unlock()

	return msg, nil
}

func (s *NatsJetStreamSource) Ack(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	m, ok := s.unackedMsgs[msg.ID()]
	if ok {
		delete(s.unackedMsgs, msg.ID())
	}
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("message not found for ack: %s", msg.ID())
	}

	return m.Ack(nats.Context(ctx))
}

func (s *NatsJetStreamSource) Ping(ctx context.Context) error {
	s.mu.Lock()
	nc := s.nc
	s.mu.Unlock()

	if nc != nil && nc.IsConnected() {
		return nil
	}

	// Just try to connect and close immediately, don't trigger JS context or subscriptions
	nc, err := nats.Connect(s.url, s.opts...)
	if err != nil {
		return fmt.Errorf("failed to connect to NATS for ping: %w", err)
	}
	nc.Close()
	return nil
}

func (s *NatsJetStreamSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	if err := s.ensureConnected(); err != nil {
		return nil, err
	}

	// We set a timeout to avoid blocking forever if the subject is empty
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// For sampling, we create an ephemeral subscription to avoid affecting existing consumer groups
	sub, err := s.js.SubscribeSync(s.subject, nats.ManualAck(), nats.Context(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe for sampling: %w", err)
	}
	defer sub.Unsubscribe()

	m, err := sub.NextMsgWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sample message from NATS: %w", err)
	}

	msg := message.AcquireMessage()
	// Use NATS sequence as ID if possible
	msgID := m.Subject
	metadata, _ := m.Metadata()
	if metadata != nil {
		msgID = fmt.Sprintf("%d", metadata.Sequence.Stream)
	}
	if msgID == "" {
		msgID = m.Subject + "_" + fmt.Sprintf("%x", m.Data)
	}

	msg.SetID(msgID)
	msg.SetPayload(m.Data)

	var jsonData map[string]any
	if err := json.Unmarshal(m.Data, &jsonData); err == nil {
		for k, v := range jsonData {
			msg.SetData(k, v)
		}
	} else {
		msg.SetAfter(m.Data)
	}

	msg.SetMetadata("nats_subject", m.Subject)
	msg.SetMetadata("sample", "true")

	// Note: We DON'T ack the message when sampling

	return msg, nil
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
