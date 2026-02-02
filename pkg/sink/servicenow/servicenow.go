package servicenow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/hermod"
)

type Config struct {
	InstanceURL string `json:"instance_url"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Table       string `json:"table"`
}

type Sink struct {
	config Config
	client *http.Client
}

func NewSink(config Config) *Sink {
	return &Sink{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	url := fmt.Sprintf("%s/api/now/table/%s", s.config.InstanceURL, s.config.Table)

	data := msg.Data()
	payload, _ := json.Marshal(data)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(s.config.Username, s.config.Password)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("servicenow api error (%d): %s", resp.StatusCode, string(body))
	}

	return nil
}

func (s *Sink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Simple sequential write for now, ServiceNow batch API is complex
	for _, msg := range msgs {
		if err := s.Write(ctx, msg); err != nil {
			return err
		}
	}
	return nil
}

func (s *Sink) Ping(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/now/table/%s?sysparm_limit=1", s.config.InstanceURL, s.config.Table)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.SetBasicAuth(s.config.Username, s.config.Password)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("servicenow connection failed: %d", resp.StatusCode)
	}
	return nil
}

func (s *Sink) Close() error {
	return nil
}
