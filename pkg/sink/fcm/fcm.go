package fcm

import (
	"context"
	"fmt"
	"sync"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/user/hermod"
	"google.golang.org/api/option"
)

type FCMSink struct {
	client          *messaging.Client
	formatter       hermod.Formatter
	credentialsJSON string
	mu              sync.Mutex
}

func NewFCMSink(credentialsJSON string, formatter hermod.Formatter) (*FCMSink, error) {
	return &FCMSink{
		formatter:       formatter,
		credentialsJSON: credentialsJSON,
	}, nil
}

func (s *FCMSink) ensureConnected(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return nil
	}

	var opts []option.ClientOption
	if s.credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(s.credentialsJSON)))
	}

	app, err := firebase.NewApp(ctx, nil, opts...)
	if err != nil {
		return fmt.Errorf("failed to initialize firebase app: %w", err)
	}

	client, err := app.Messaging(ctx)
	if err != nil {
		return fmt.Errorf("failed to create fcm client: %w", err)
	}

	s.client = client
	return nil
}

func (s *FCMSink) Write(ctx context.Context, msg hermod.Message) error {
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
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}
	// Dry run with a dummy message to check if client/auth is working
	// In firebase-admin-go v4, we can use client.Send(ctx, msg) and it might fail with "registration-token-not-registered"
	// which actually means the client and auth are working fine.
	_, err := s.client.Send(ctx, &messaging.Message{
		Token: "dry-run-token",
	})

	// If the error is about invalid token, it means we reached FCM, so "ping" is successful.
	if err != nil && (messaging.IsRegistrationTokenNotRegistered(err) || messaging.IsInvalidArgument(err)) {
		return nil
	}

	return err
}

func (s *FCMSink) Close() error {
	// firebase messaging client doesn't have a Close method
	return nil
}
