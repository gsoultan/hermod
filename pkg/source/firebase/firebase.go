package firebase

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type FirebaseSource struct {
	projectID       string
	collection      string
	credentialsJSON string
	timestampField  string
	pollInterval    time.Duration
	lastTimestamp   time.Time
	logger          hermod.Logger
	app             *firebase.App
	client          *firestore.Client
}

func NewFirebaseSource(projectID, collection, credentialsJSON, timestampField string, pollInterval time.Duration) *FirebaseSource {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Minute
	}
	if timestampField == "" {
		timestampField = "updated_at"
	}
	return &FirebaseSource{
		projectID:       projectID,
		collection:      collection,
		credentialsJSON: credentialsJSON,
		timestampField:  timestampField,
		pollInterval:    pollInterval,
	}
}

func (s *FirebaseSource) SetLogger(logger hermod.Logger) {
	s.logger = logger
}

func (s *FirebaseSource) init(ctx context.Context) error {
	if s.client != nil {
		return nil
	}

	var opts []option.ClientOption
	if s.credentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(s.credentialsJSON)))
	}

	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: s.projectID}, opts...)
	if err != nil {
		return fmt.Errorf("failed to initialize firebase app: %w", err)
	}
	s.app = app

	client, err := app.Firestore(ctx)
	if err != nil {
		return fmt.Errorf("failed to create firestore client: %w", err)
	}
	s.client = client

	return nil
}

func (s *FirebaseSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		query := s.client.Collection(s.collection).
			Where(s.timestampField, ">", s.lastTimestamp).
			OrderBy(s.timestampField, firestore.Asc).
			Limit(1)

		iter := query.Documents(ctx)
		doc, err := iter.Next()
		if err == iterator.Done {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ticker.C:
				continue
			}
		}
		if err != nil {
			if s.logger != nil {
				s.logger.Error("Failed to fetch firestore documents", "error", err)
			}
			time.Sleep(5 * time.Second)
			continue
		}

		data := doc.Data()
		if ts, ok := data[s.timestampField].(time.Time); ok {
			s.lastTimestamp = ts
		}

		payload, _ := json.Marshal(data)
		msg := message.AcquireMessage()
		msg.SetID(doc.Ref.ID)
		msg.SetOperation(hermod.OpUpdate)
		msg.SetTable(s.collection)
		msg.SetAfter(payload)
		msg.SetMetadata("source", "firebase")
		msg.SetMetadata("doc_id", doc.Ref.ID)
		msg.SetMetadata("project_id", s.projectID)

		return msg, nil
	}
}

func (s *FirebaseSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *FirebaseSource) Ping(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	// Try to get one document to ping
	iter := s.client.Collection(s.collection).Limit(1).Documents(ctx)
	_, err := iter.Next()
	if err == iterator.Done {
		return nil
	}
	return err
}

func (s *FirebaseSource) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

func (s *FirebaseSource) GetState() map[string]string {
	return map[string]string{
		"last_timestamp": s.lastTimestamp.Format(time.RFC3339Nano),
	}
}

func (s *FirebaseSource) SetState(state map[string]string) {
	if val, ok := state["last_timestamp"]; ok {
		s.lastTimestamp, _ = time.Parse(time.RFC3339Nano, val)
	}
}
