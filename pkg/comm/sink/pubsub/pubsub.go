package pubsub

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/gsoultan/hermod"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PubSubSink struct {
	client          *pubsub.Client
	publisher       *pubsub.Publisher
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
	if s.client != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	var opts []option.ClientOption
	if s.credentialsJSON != "" {
		opts = append(opts, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(s.credentialsJSON)))
	}
	client, err := pubsub.NewClient(ctx, s.projectID, opts...)
	if err != nil {
		return fmt.Errorf("failed to create pubsub client: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		// Another goroutine won the race; this client is surplus.
		_ = client.Close()
		return nil
	}

	s.client = client
	s.publisher = client.Publisher(s.topicID)

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
	maps.Copy(pubMsg.Attributes, msg.Metadata())

	result := s.publisher.Publish(ctx, pubMsg)

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

	// v2 dropped Topic.Exists; the admin client answers it instead. A NotFound
	// is the "this topic is missing" answer rather than a transport failure, and
	// the two need saying differently: one is a configuration mistake an operator
	// can fix, the other is the sink being unable to reach Pub/Sub at all.
	_, err := s.client.TopicAdminClient.GetTopic(ctx, &pubsubpb.GetTopicRequest{
		Topic: fmt.Sprintf("projects/%s/topics/%s", s.projectID, s.publisher.ID()),
	})
	if status.Code(err) == codes.NotFound {
		return fmt.Errorf("topic %s does not exist in project %s", s.publisher.ID(), s.projectID)
	}
	if err != nil {
		return fmt.Errorf("failed to check if topic exists: %w", err)
	}
	return nil
}

func (s *PubSubSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.publisher != nil {
		s.publisher.Stop()
	}
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}
