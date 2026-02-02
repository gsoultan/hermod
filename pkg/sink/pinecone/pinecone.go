package pinecone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod"
)

// Config represents the Pinecone sink configuration.
type Config struct {
	APIKey      string `json:"api_key"`
	Environment string `json:"environment"`
	IndexName   string `json:"index_name"`
	Namespace   string `json:"namespace"`
	Dimension   int    `json:"dimension"`
}

// Sink implements the hermod.Sink interface for Pinecone.
type Sink struct {
	config Config
	client *http.Client
}

// NewSink creates a new Pinecone sink.
func NewSink(cfg Config) *Sink {
	return &Sink{
		config: cfg,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Write writes a single message to Pinecone.
func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	return s.WriteBatch(ctx, []hermod.Message{msg})
}

// Ping checks if the Pinecone index is accessible.
func (s *Sink) Ping(ctx context.Context) error {
	// Simplified ping: check if we can get index info
	url := fmt.Sprintf("https://controller.%s.pinecone.io/databases/%s", s.config.Environment, s.config.IndexName)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Api-Key", s.config.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pinecone ping failed with status: %d", resp.StatusCode)
	}

	return nil
}

// WriteBatch writes a batch of messages to Pinecone.
// It expects the messages to have 'id', 'values' (float array), and optional 'metadata'.
func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	type Vector struct {
		ID       string                 `json:"id"`
		Values   []float64              `json:"values"`
		Metadata map[string]interface{} `json:"metadata,omitempty"`
	}

	type UpsertRequest struct {
		Vectors   []Vector `json:"vectors"`
		Namespace string   `json:"namespace,omitempty"`
	}

	vectors := make([]Vector, 0, len(msgs))
	for _, msg := range msgs {
		var v Vector
		data := msg.Data()

		// Map ID
		if id, ok := data["id"].(string); ok {
			v.ID = id
		} else {
			v.ID = msg.ID()
		}

		// Map Values (Embeddings)
		if vals, ok := data["values"].([]interface{}); ok {
			v.Values = make([]float64, len(vals))
			for i, val := range vals {
				if f, ok := val.(float64); ok {
					v.Values[i] = f
				} else if iVal, ok := val.(int); ok {
					v.Values[i] = float64(iVal)
				}
			}
		} else if vals, ok := data["values"].([]float64); ok {
			v.Values = vals
		}

		// Map Metadata
		if meta, ok := data["metadata"].(map[string]interface{}); ok {
			v.Metadata = meta
		}

		if len(v.Values) > 0 {
			vectors = append(vectors, v)
		}
	}

	if len(vectors) == 0 {
		return nil
	}

	reqBody := UpsertRequest{
		Vectors:   vectors,
		Namespace: s.config.Namespace,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal pinecone request: %w", err)
	}

	// Host URL calculation (simplified for example)
	url := fmt.Sprintf("https://%s-%s.svc.%s.pinecone.io/vectors/upsert", s.config.IndexName, s.config.Environment, s.config.Environment)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Api-Key", s.config.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pinecone upsert failed with status: %d", resp.StatusCode)
	}

	return nil
}

func (s *Sink) Close() error {
	return nil
}
