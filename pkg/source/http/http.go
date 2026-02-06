package sourcehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// HTTPSource implements the hermod.Source interface for polling HTTP endpoints.
type HTTPSource struct {
	url           string
	method        string
	headers       map[string]string
	interval      time.Duration
	dataPath      string // Path to extract array from JSON response using GJSON
	lastTimestamp time.Time
	client        *http.Client
	items         []interface{}
	currentIndex  int
}

func NewHTTPSource(url, method string, headers map[string]string, interval time.Duration, dataPath string) *HTTPSource {
	if method == "" {
		method = "GET"
	}
	if interval == 0 {
		interval = 60 * time.Second
	}
	return &HTTPSource{
		url:      url,
		method:   method,
		headers:  headers,
		interval: interval,
		dataPath: dataPath,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (s *HTTPSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.currentIndex < len(s.items) {
		item := s.items[s.currentIndex]
		s.currentIndex++
		return s.messageFromData(item), nil
	}

	// Simple polling logic: wait for interval
	if !s.lastTimestamp.IsZero() {
		nextRun := s.lastTimestamp.Add(s.interval)
		if time.Now().Before(nextRun) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Until(nextRun)):
			}
		}
	}

	s.lastTimestamp = time.Now()
	s.items = nil
	s.currentIndex = 0

	req, err := http.NewRequestWithContext(ctx, s.method, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http request failed: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if s.dataPath != "" {
		result := gjson.GetBytes(body, s.dataPath)
		if result.IsArray() {
			for _, item := range result.Array() {
				s.items = append(s.items, item.Value())
			}
		} else if result.Exists() {
			s.items = append(s.items, result.Value())
		}
	} else {
		var data interface{}
		if err := json.Unmarshal(body, &data); err != nil {
			// If not JSON, return as raw string in a map
			data = map[string]interface{}{"raw": string(body)}
		}

		if arr, ok := data.([]interface{}); ok {
			s.items = arr
		} else {
			s.items = append(s.items, data)
		}
	}

	if len(s.items) == 0 {
		// If no items found, we could either return an empty message or wait for next poll
		// Most Hermod sources block until data is available.
		// For now, let's recurse (it will wait for interval in the next call)
		return s.Read(ctx)
	}

	item := s.items[0]
	s.currentIndex = 1
	return s.messageFromData(item), nil
}

func (s *HTTPSource) messageFromData(data interface{}) hermod.Message {
	msg := message.AcquireMessage()
	msg.SetID(uuid.New().String())
	msg.SetMetadata("source", "http")
	msg.SetMetadata("url", s.url)

	if m, ok := data.(map[string]interface{}); ok {
		for k, v := range m {
			msg.SetData(k, v)
		}
	} else {
		msg.SetData("value", data)
	}
	return msg
}

func (s *HTTPSource) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *HTTPSource) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", s.url, nil)
	if err != nil {
		return err
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("ping failed: status %d", resp.StatusCode)
	}
	return nil
}

func (s *HTTPSource) Close() error {
	return nil
}
