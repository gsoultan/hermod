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

func (c *WorkerAPIClient) ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error) {
	url := "/api/workflows"
	if filter.Limit > 0 {
		url = fmt.Sprintf("%s?page=%d&limit=%d&search=%s", url, filter.Page, filter.Limit, filter.Search)
	} else if filter.Limit == -1 {
		url = fmt.Sprintf("%s?limit=0", url) // 0 means no limit in our storage implementation
	}

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("API error: %s", resp.Status)
	}

	var res struct {
		Data  []storage.Workflow `json:"data"`
		Total int                `json:"total"`
	}
	err = json.NewDecoder(resp.Body).Decode(&res)
	return res.Data, res.Total, err
}

func (c *WorkerAPIClient) GetWorkflow(ctx context.Context, id string) (storage.Workflow, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/api/workflows/%s", id), nil)
	if err != nil {
		return storage.Workflow{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return storage.Workflow{}, fmt.Errorf("API error: %s", resp.Status)
	}

	var wf storage.Workflow
	err = json.NewDecoder(resp.Body).Decode(&wf)
	return wf, err
}

func (c *WorkerAPIClient) UpdateWorkflow(ctx context.Context, wf storage.Workflow) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/workflows/%s", wf.ID), wf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
}

func (c *WorkerAPIClient) UpdateWorkflowStatus(ctx context.Context, id string, status string) error {
	payload := map[string]string{"status": status}
	resp, err := c.doRequest(ctx, "PATCH", fmt.Sprintf("/api/workflows/%s/status", id), payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
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

func (c *WorkerAPIClient) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	url := "/api/sources"
	if filter.Limit > 0 {
		url = fmt.Sprintf("%s?page=%d&limit=%d&search=%s", url, filter.Page, filter.Limit, filter.Search)
	}

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("API error: %s", resp.Status)
	}

	var res struct {
		Data  []storage.Source `json:"data"`
		Total int              `json:"total"`
	}
	err = json.NewDecoder(resp.Body).Decode(&res)
	return res.Data, res.Total, err
}

func (c *WorkerAPIClient) ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error) {
	url := "/api/sinks"
	if filter.Limit > 0 {
		url = fmt.Sprintf("%s?page=%d&limit=%d&search=%s", url, filter.Page, filter.Limit, filter.Search)
	}

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("API error: %s", resp.Status)
	}

	var res struct {
		Data  []storage.Sink `json:"data"`
		Total int            `json:"total"`
	}
	err = json.NewDecoder(resp.Body).Decode(&res)
	return res.Data, res.Total, err
}

func (c *WorkerAPIClient) UpdateSource(ctx context.Context, src storage.Source) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/sources/%s", src.ID), src)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
}

func (c *WorkerAPIClient) UpdateSink(ctx context.Context, snk storage.Sink) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/api/sinks/%s", snk.ID), snk)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
}

func (c *WorkerAPIClient) UpdateWorkerHeartbeat(ctx context.Context, id string, cpu, mem float64) error {
	payload := map[string]float64{
		"cpu_usage":    cpu,
		"memory_usage": mem,
	}
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/api/workers/%s/heartbeat", id), payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
}

func (c *WorkerAPIClient) ListWorkers(ctx context.Context, filter storage.CommonFilter) ([]storage.Worker, int, error) {
	url := "/api/workers"
	if filter.Limit > 0 {
		url = fmt.Sprintf("%s?page=%d&limit=%d&search=%s", url, filter.Page, filter.Limit, filter.Search)
	}

	resp, err := c.doRequest(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("API error: %s", resp.Status)
	}

	var res struct {
		Data  []storage.Worker `json:"data"`
		Total int              `json:"total"`
	}
	err = json.NewDecoder(resp.Body).Decode(&res)
	return res.Data, res.Total, err
}

func (c *WorkerAPIClient) CreateLog(ctx context.Context, log storage.Log) error {
	resp, err := c.doRequest(ctx, "POST", "/api/logs", log)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error: %s", resp.Status)
	}
	return nil
}

// Lease APIs (stubs for platform client for now). Platform endpoints can be
// added later; returning false keeps behavior conservative when using the API
// client as storage.
func (c *WorkerAPIClient) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return false, nil
}

func (c *WorkerAPIClient) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return false, nil
}

func (c *WorkerAPIClient) ReleaseWorkflowLease(ctx context.Context, workflowID, ownerID string) error {
	return nil
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
