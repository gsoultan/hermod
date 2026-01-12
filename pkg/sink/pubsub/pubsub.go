package pubsub

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub"
	"github.com/user/hermod"
	"google.golang.org/api/option"
)

type PubSubSink struct {
	client    *pubsub.Client
	topic     *pubsub.Topic
	formatter hermod.Formatter
}

func NewPubSubSink(projectID string, topicID string, credentialsJSON string, formatter hermod.Formatter) (*PubSubSink, error) {
	ctx := context.Background()
	var opts []option.ClientOption
	if credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credentialsJSON)))
	}
	client, err := pubsub.NewClient(ctx, projectID, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create pubsub client: %w", err)
	}

	topic := client.Topic(topicID)

	return &PubSubSink{
		client:    client,
		topic:     topic,
		formatter: formatter,
	}, nil
}

func (s *PubSubSink) Write(ctx context.Context, msg hermod.Message) error {
	data, err := s.formatter.Format(msg)
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
	s.topic.Stop()
	return s.client.Close()
}
