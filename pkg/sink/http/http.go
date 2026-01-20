package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/user/hermod"
)

type HttpSink struct {
	url        string
	client     *http.Client
	formatter  hermod.Formatter
	headers    map[string]string
	pingMethod string
}

func NewHttpSink(url string, formatter hermod.Formatter, headers map[string]string) *HttpSink {
	return &HttpSink{
		url:        url,
		client:     &http.Client{},
		formatter:  formatter,
		headers:    headers,
		pingMethod: "HEAD",
	}
}

func (s *HttpSink) SetPingMethod(method string) {
	s.pingMethod = method
}

func (s *HttpSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
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

	req, err := http.NewRequestWithContext(ctx, "POST", s.url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

func (s *HttpSink) WriteBatch(ctx context.Context, msgs []hermod.Message) error {
	// Filter nil messages
	filtered := make([]hermod.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			filtered = append(filtered, m)
		}
	}
	msgs = filtered

	if len(msgs) == 0 {
		return nil
	}

	// Try to see if we can format as a JSON array of messages
	var payload []byte
	var err error

	if s.formatter != nil {
		// If we have a formatter, we don't know if it supports batching.
		// For now, we'll fall back to individual writes if we can't do a simple JSON batch.
		// However, many users might expect a JSON array.

		// Let's try to format each message and join them in a JSON array if they look like JSON
		formattedMsgs := make([][]byte, 0, len(msgs))
		for _, msg := range msgs {
			data, err := s.formatter.Format(msg)
			if err != nil {
				return fmt.Errorf("failed to format message in batch: %w", err)
			}
			formattedMsgs = append(formattedMsgs, data)
		}

		// Check if we can just wrap them in [ ... ]
		// This assumes each formatted message is a valid JSON object.
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, data := range formattedMsgs {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(data)
		}
		buf.WriteByte(']')
		payload = buf.Bytes()
	} else {
		// Use raw payloads
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, msg := range msgs {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.Write(msg.Payload())
		}
		buf.WriteByte(']')
		payload = buf.Bytes()
	}

	req, err := http.NewRequestWithContext(ctx, "POST", s.url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create batch request: %w", err)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send batch request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("batch request failed with status code: %d", resp.StatusCode)
	}

	return nil
}

func (s *HttpSink) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, s.pingMethod, s.url, nil)
	if err != nil {
		return fmt.Errorf("failed to create ping request: %w", err)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send ping request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ping failed with status code: %d", resp.StatusCode)
	}

	return nil
}

func (s *HttpSink) Close() error {
	s.client.CloseIdleConnections()
	return nil
}
