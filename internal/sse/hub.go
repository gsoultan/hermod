package sse

import (
	"context"
	"sync"
	"time"
)

// Event represents a server-sent event payload.
type Event struct {
	ID    string
	Event string
	Data  []byte
}

// Hub is a simple in-memory pub/sub for SSE topics.
type Hub struct {
	mu       sync.RWMutex
	subs     map[string]map[chan Event]struct{}
	configs  map[string]StreamConfig
	shutdown chan struct{}
}

// StreamConfig holds security and other settings for an SSE stream.
type StreamConfig struct {
	AuthToken      string
	AllowedOrigins []string
}

var (
	internalHub  *Hub
	internalOnce sync.Once
	dataHub      *Hub
	dataOnce     sync.Once
)

// GetInternalHub returns the SSE hub for internal API notifications.
func GetInternalHub() *Hub {
	internalOnce.Do(func() {
		internalHub = &Hub{
			subs:     make(map[string]map[chan Event]struct{}),
			configs:  make(map[string]StreamConfig),
			shutdown: make(chan struct{}),
		}
	})
	return internalHub
}

// GetDataHub returns the SSE hub for data orchestration (SSESink).
func GetDataHub() *Hub {
	dataOnce.Do(func() {
		dataHub = &Hub{
			subs:     make(map[string]map[chan Event]struct{}),
			configs:  make(map[string]StreamConfig),
			shutdown: make(chan struct{}),
		}
	})
	return dataHub
}

// Publish sends an event to all subscribers of the topic.
func (h *Hub) Publish(topic string, ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if subs, ok := h.subs[topic]; ok {
		for ch := range subs {
			select {
			case ch <- ev:
			default:
				// Drop if subscriber is slow to avoid blocking writers
			}
		}
	}
}

// Subscribe adds a subscriber channel for the topic.
// Returns the channel and an unsubscribe function.
func (h *Hub) Subscribe(topic string, buf int) (chan Event, func()) {
	if buf <= 0 {
		buf = 16
	}
	ch := make(chan Event, buf)

	h.mu.Lock()
	if _, ok := h.subs[topic]; !ok {
		h.subs[topic] = make(map[chan Event]struct{})
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()

	unsub := func() {
		h.mu.Lock()
		if subs, ok := h.subs[topic]; ok {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subs, topic)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, unsub
}

// Stream streams events from a topic to the provided callback until context is done.
func (h *Hub) Stream(ctx context.Context, topic string, buf int, fn func(Event) error) error {
	ch, unsub := h.Subscribe(topic, buf)
	defer unsub()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := fn(ev); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-h.shutdown:
			return nil
		}
	}
}

// Shutdown stops the hub and closes all subscribers.
func (h *Hub) Shutdown(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.shutdown:
		// already closed
	default:
		close(h.shutdown)
	}
	for topic, subs := range h.subs {
		for ch := range subs {
			close(ch)
		}
		delete(h.subs, topic)
	}
}

// WaitUntil waits until there is at least one subscriber for the topic or timeout.
func (h *Hub) WaitUntil(topic string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		h.mu.RLock()
		count := len(h.subs[topic])
		h.mu.RUnlock()
		if count > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// ConfigureStream sets the configuration for a specific stream.
func (h *Hub) ConfigureStream(topic string, cfg StreamConfig) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.configs == nil {
		h.configs = make(map[string]StreamConfig)
	}
	h.configs[topic] = cfg
}

// GetStreamConfig returns the configuration for a specific stream.
func (h *Hub) GetStreamConfig(topic string) (StreamConfig, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	cfg, ok := h.configs[topic]
	return cfg, ok
}
