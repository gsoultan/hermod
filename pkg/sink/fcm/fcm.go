package fcm

import (
	"context"
	"fmt"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/user/hermod"
	"google.golang.org/api/option"
)

type FCMSink struct {
	client    *messaging.Client
	formatter hermod.Formatter
}

func NewFCMSink(credentialsJSON string, formatter hermod.Formatter) (*FCMSink, error) {
	ctx := context.Background()
	var opts []option.ClientOption
	if credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(credentialsJSON)))
	}

	app, err := firebase.NewApp(ctx, nil, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize firebase app: %w", err)
	}

	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create fcm client: %w", err)
	}

	return &FCMSink{
		client:    client,
		formatter: formatter,
	}, nil
}

func (s *FCMSink) Write(ctx context.Context, msg hermod.Message) error {
	data, err := s.formatter.Format(msg)
	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	// FCM messages can be sent to a specific token, a topic, or a condition.
	// We'll look for "fcm_token" or "fcm_topic" in the message metadata.
	// If neither is present, we might want to skip or return an error.

	fcmMsg := &messaging.Message{
		Data: map[string]string{
			"payload":   string(data),
			"id":        msg.ID(),
			"operation": string(msg.Operation()),
			"table":     msg.Table(),
			"schema":    msg.Schema(),
		},
	}

	metadata := msg.Metadata()
	if token, ok := metadata["fcm_token"]; ok {
		fcmMsg.Token = token
	} else if topic, ok := metadata["fcm_topic"]; ok {
		fcmMsg.Topic = topic
	} else if condition, ok := metadata["fcm_condition"]; ok {
		fcmMsg.Condition = condition
	} else {
		// If no destination is specified, we can't send the message.
		// For now, we'll return an error, but it could also be a no-op depending on requirements.
		return fmt.Errorf("fcm destination (token, topic, or condition) not found in message metadata")
	}

	// Optionally add notification details if present in metadata
	if title, ok := metadata["fcm_notification_title"]; ok {
		if fcmMsg.Notification == nil {
			fcmMsg.Notification = &messaging.Notification{}
		}
		fcmMsg.Notification.Title = title
	}
	if body, ok := metadata["fcm_notification_body"]; ok {
		if fcmMsg.Notification == nil {
			fcmMsg.Notification = &messaging.Notification{}
		}
		fcmMsg.Notification.Body = body
	}

	_, err = s.client.Send(ctx, fcmMsg)
	if err != nil {
		return fmt.Errorf("failed to send fcm message: %w", err)
	}

	return nil
}

func (s *FCMSink) Ping(ctx context.Context) error {
	// FCM doesn't have a direct ping, but we can try to send a dry run message
	// or just check if the client is initialized.
	// Since we don't have a valid token here, a dry run might fail.
	// For now, we just check if client is not nil.
	if s.client == nil {
		return fmt.Errorf("fcm client is not initialized")
	}
	return nil
}

func (s *FCMSink) Close() error {
	// firebase messaging client doesn't have a Close method
	return nil
}
