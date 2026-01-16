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
