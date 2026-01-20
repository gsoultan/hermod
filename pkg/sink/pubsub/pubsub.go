package pubsub

import (
	"context"
	"fmt"
	"sync"

	"cloud.google.com/go/pubsub"
	"github.com/user/hermod"
	"google.golang.org/api/option"
)

type PubSubSink struct {
	client          *pubsub.Client
	topic           *pubsub.Topic
	formatter       hermod.Formatter
	projectID       string
	topicID         string
	credentialsJSON string
	mu              sync.Mutex
}

func NewPubSubSink(projectID string, topicID string, credentialsJSON string, formatter hermod.Formatter) (*PubSubSink, error) {
	return &PubSubSink{
		projectID:       projectID,
		topicID:         topicID,
		credentialsJSON: credentialsJSON,
		formatter:       formatter,
	}, nil
}

func (s *PubSubSink) ensureConnected(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	var opts []option.ClientOption
	if s.credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(s.credentialsJSON)))
	}
	client, err := pubsub.NewClient(ctx, s.projectID, opts...)
	if err != nil {
		return fmt.Errorf("failed to create pubsub client: %w", err)
	}

	s.client = client
	s.topic = client.Topic(s.topicID)

	return nil
}

func (s *PubSubSink) Write(ctx context.Context, msg hermod.Message) error {
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
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	pubMsg := &pubsub.Message{
		Data: data,
		Attributes: map[string]string{
			"id":        msg.ID(),
			"operation": string(msg.Operation()),
			"table":     msg.Table(),
			"schema":    msg.Schema(),
		},
	}

	// Copy other metadata if available
	for k, v := range msg.Metadata() {
		pubMsg.Attributes[k] = v
	}

	result := s.topic.Publish(ctx, pubMsg)

	_, err = result.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to publish message to pubsub: %w", err)
	}

	return nil
}

func (s *PubSubSink) Ping(ctx context.Context) error {
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	exists, err := s.topic.Exists(ctx)
	if err != nil {
		return fmt.Errorf("failed to check if topic exists: %w", err)
	}
	if !exists {
		return fmt.Errorf("topic %s does not exist", s.topic.ID())
	}
	return nil
}

func (s *PubSubSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.topic != nil {
		s.topic.Stop()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
