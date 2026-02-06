package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/internal/sse"
)

// SSESink publishes messages to an in-process SSE hub under a named stream.
// Clients can subscribe via the API endpoint to receive realtime events.
type SSESink struct {
	stream    string
	formatter hermod.Formatter
	logger    hermod.Logger
	bufSize   int
}

func NewSSESink(stream string, formatter hermod.Formatter) *SSESink {
	if strings.TrimSpace(stream) == "" {
		stream = "default"
	}
	return &SSESink{stream: stream, formatter: formatter, bufSize: 64}
}

func (s *SSESink) SetLogger(l hermod.Logger) { s.logger = l }

func (s *SSESink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	var data []byte
	var err error
	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else if p := msg.Payload(); len(p) > 0 {
		data = p
	} else {
		// Fallback to JSON of Data map
		data, err = json.Marshal(msg.Data())
	}
	if err != nil {
		return fmt.Errorf("sse sink: format message: %w", err)
	}

	// Optional: include message id and type as SSE fields
	ev := sse.Event{
		ID:    msg.ID(),
		Event: string(msg.Operation()),
		Data:  data,
	}
	sse.GetHub().Publish(s.stream, ev)

	if s.logger != nil {
		s.logger.Debug("SSE published", "stream", s.stream, "message_id", msg.ID())
	}
	return nil
}

func (s *SSESink) Ping(ctx context.Context) error {
	// Ping succeeds if hub is available; optionally ensure at least one subscriber in last 1s
	_ = sse.GetHub()
	return nil
}

func (s *SSESink) Close() error { return nil }

// WithBuffer allows overriding per-sink subscriber buffer size.
func (s *SSESink) WithBuffer(n int) *SSESink { s.bufSize = n; return s }

// WaitForSubscribers waits for at least one subscriber on the stream up to d.
func (s *SSESink) WaitForSubscribers(d time.Duration) bool {
	return sse.GetHub().WaitUntil(s.stream, d)
}
