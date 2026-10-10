package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// maxResponseBody bounds a successful provider response.
	maxResponseBody = 32 << 20
	// maxErrorBody bounds what an error response contributes to an error.
	maxErrorBody = 2 << 10
)

// DefaultHTTPClient is used by the HTTP adapters when given no client. The
// timeout is a backstop; callers bound each call with their context.
var DefaultHTTPClient = &http.Client{Timeout: 5 * time.Minute}

// PostJSON sends body as JSON to url and decodes the response into out. A
// non-2xx answer becomes an *APIError carrying a bounded excerpt of the body;
// network failures are retryable APIErrors. Context cancellation is returned
// as the context's error, not as an APIError.
func PostJSON(ctx context.Context, client *http.Client, provider, url string, headers map[string]string, body, out any) (http.Header, error) {
	if client == nil {
		client = DefaultHTTPClient
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", provider, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", provider, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &APIError{Provider: provider, Message: err.Error(), Retryable: true}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return resp.Header, &APIError{
			Provider:   provider,
			Status:     resp.StatusCode,
			Message:    errorMessage(raw),
			Retryable:  RetryableStatus(resp.StatusCode),
			RetryAfter: RetryAfter(resp.Header),
		}
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody))
	if err := dec.Decode(out); err != nil {
		return resp.Header, fmt.Errorf("%s: decode response: %w", provider, err)
	}
	return resp.Header, nil
}

// errorMessage pulls the human-readable message out of the common error
// shapes ({"error":{"message":...}}, {"error":"..."}, {"message":...}) and
// falls back to the raw text.
func errorMessage(raw []byte) string {
	var shaped struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &shaped) == nil {
		var nested struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(shaped.Error, &nested) == nil && nested.Message != "" {
			return nested.Message
		}
		var plain string
		if json.Unmarshal(shaped.Error, &plain) == nil && plain != "" {
			return plain
		}
		if shaped.Message != "" {
			return shaped.Message
		}
	}
	return strings.TrimSpace(string(raw))
}

// RetryAfter reads a Retry-After header given in seconds. HTTP dates and
// absent or malformed headers give zero.
func RetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 0
}

// ErrRequiresModel is returned by adapters that have no default model.
func ErrRequiresModel(provider string) error {
	return fmt.Errorf("%s: a model is required", provider)
}

// ErrEmptyRequest is returned for a chat request with no messages.
var ErrEmptyRequest = errors.New("llm: request has no messages")
