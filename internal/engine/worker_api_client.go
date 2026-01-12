package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod/internal/storage"
)

// WorkerAPIClient handles communication with the Hermod platform API.
type WorkerAPIClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

func NewWorkerAPIClient(baseURL string, token string) *WorkerAPIClient {
	return &WorkerAPIClient{
		BaseURL: baseURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *WorkerAPIClient) GetWorker(ctx context.Context, id string) (storage.Worker, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/workers/%s", id), nil)
	if err != nil {
		return storage.Worker{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return storage.Worker{}, storage.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return storage.Worker{}, fmt.Errorf("API error: %s", resp.Status)
	}

	var w storage.Worker
	err = json.NewDecoder(resp.Body).Decode(&w)
	return w, err
}

func (c *WorkerAPIClient) CreateWorker(ctx context.Context, w storage.Worker) error {
	resp, err := c.doRequest(ctx, "POST", "/api/workers", w)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
}

func (c *WorkerAPIClient) ListConnections(ctx context.Context) ([]storage.Connection, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/connections", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error: %s", resp.Status)
	}

	var connections []storage.Connection
	err = json.NewDecoder(resp.Body).Decode(&connections)
	return connections, err
}

func (c *WorkerAPIClient) GetSource(ctx context.Context, id string) (storage.Source, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/sources/%s", id), nil)
	if err != nil {
		return storage.Source{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return storage.Source{}, fmt.Errorf("API error: %s", resp.Status)
	}

	var s storage.Source
	err = json.NewDecoder(resp.Body).Decode(&s)
	return s, err
}

func (c *WorkerAPIClient) GetSink(ctx context.Context, id string) (storage.Sink, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/sinks/%s", id), nil)
	if err != nil {
		return storage.Sink{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return storage.Sink{}, fmt.Errorf("API error: %s", resp.Status)
	}

	var s storage.Sink
	err = json.NewDecoder(resp.Body).Decode(&s)
	return s, err
}

func (c *WorkerAPIClient) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var bodyReader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	url := fmt.Sprintf("%s%s", c.BaseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if c.Token != "" {
		req.Header.Set("X-Worker-Token", c.Token)
	}

	return c.HTTPClient.Do(req)
}
