package mqtt

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/comm/message"
)

// Source implements hermod.Source for MQTT brokers using Eclipse Paho.
// It supports:
// - Multiple topic subscriptions (comma-separated filters)
// - QoS 0/1/2
// - TLS with system roots and optional InsecureSkipVerify
// - Auto reconnect with configurable max interval
// - Graceful shutdown
type Source struct {
	mu     sync.RWMutex
	cfg    map[string]string
	client paho.Client
	opts   *paho.ClientOptions
	topics []string
	qos    byte
	msgCh  chan hermod.Message
	closed bool
}

// buildClientOptions translates the raw config map into Paho client options plus
// the resolved topic filters and QoS. It is shared by NewSource and Sample so a
// preview connects with exactly the same broker/auth/TLS settings as ingestion.
func buildClientOptions(cfg map[string]string) (*paho.ClientOptions, []string, byte, error) {
	brokerURL := strings.TrimSpace(cfg["broker_url"])
	if brokerURL == "" {
		// Backward-compat key: url
		brokerURL = strings.TrimSpace(cfg["url"])
	}
	if brokerURL == "" {
		return nil, nil, 0, errors.New("mqtt: broker_url is required")
	}

	opts := paho.NewClientOptions()
	opts.AddBroker(brokerURL)

	if cid := strings.TrimSpace(cfg["client_id"]); cid != "" {
		opts.SetClientID(cid)
	} else {
		// Let Paho generate a random client ID when empty
		opts.SetClientID("")
	}

	if u := strings.TrimSpace(cfg["username"]); u != "" {
		opts.SetUsername(u)
		opts.SetPassword(cfg["password"]) // may be empty
	}

	// Clean session default true
	cleanSession := true
	if v := strings.TrimSpace(cfg["clean_session"]); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cleanSession = b
		}
	}
	opts.SetCleanSession(cleanSession)

	// KeepAlive in seconds
	keepAlive := 30 * time.Second
	if v := strings.TrimSpace(cfg["keepalive"]); v != "" {
		if secs, err := time.ParseDuration(v); err == nil {
			keepAlive = secs
		} else if n, err := strconv.Atoi(v); err == nil {
			keepAlive = time.Duration(n) * time.Second
		}
	}
	opts.SetKeepAlive(keepAlive)

	// Reconnect configuration
	opts.AutoReconnect = true
	if v := strings.TrimSpace(cfg["max_reconnect_interval"]); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			opts.MaxReconnectInterval = d
		}
	}

	// TLS config (system roots by default)
	if strings.HasPrefix(brokerURL, "ssl://") || strings.HasPrefix(brokerURL, "tls://") || strings.HasPrefix(brokerURL, "wss://") {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		// Load system roots
		roots, err := x509.SystemCertPool()
		if err == nil && roots != nil {
			tlsCfg.RootCAs = roots
		}
		if v := strings.TrimSpace(cfg["tls_insecure_skip_verify"]); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				tlsCfg.InsecureSkipVerify = b
			}
		}
		opts.SetTLSConfig(tlsCfg)
	}

	// QoS
	qos := byte(1)
	if v := strings.TrimSpace(cfg["qos"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 2 {
			qos = byte(n)
		}
	}

	topics := parseCSV(cfg["topics"])
	if len(topics) == 0 {
		// Backward-compat single topic key
		if t := strings.TrimSpace(cfg["topic"]); t != "" {
			topics = []string{t}
		}
	}
	if len(topics) == 0 {
		return nil, nil, 0, errors.New("mqtt: at least one topic is required")
	}

	return opts, topics, qos, nil
}

// NewSource constructs a new MQTT source. Expected config keys:
// - broker_url: e.g. tcp://localhost:1883, ssl://broker:8883, ws://..., wss://...
// - topics: comma-separated list of topic filters
// - client_id: optional client identifier
// - username, password: optional auth
// - qos: 0|1|2
// - clean_session: true|false (default true)
// - keepalive: duration in seconds (default 30)
// - tls_insecure_skip_verify: true|false (default false)
// - max_reconnect_interval: duration (e.g., 30s)
func NewSource(cfg map[string]string) (*Source, error) {
	opts, topics, qos, err := buildClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	s := &Source{
		cfg:    cfg,
		opts:   opts,
		topics: topics,
		qos:    qos,
		msgCh:  make(chan hermod.Message, 1024),
	}

	// Global message handler pushes to channel
	opts.SetDefaultPublishHandler(func(_ paho.Client, m paho.Message) {
		// Defensive copy of payload
		payload := append([]byte(nil), m.Payload()...)
		msg := message.AcquireMessage()
		// Use composed ID: topic:mid:ts
		msg.SetID(fmt.Sprintf("%s:%d:%d", m.Topic(), m.MessageID(), time.Now().UnixNano()))
		msg.SetOperation(hermod.OpUpdate)
		msg.SetTable("")
		msg.SetSchema("")
		// Prefer payload bytes to avoid double JSON encoding
		msg.SetPayload(payload)
		msg.SetMetadata("topic", m.Topic())
		msg.SetMetadata("qos", strconv.Itoa(int(m.Qos())))
		msg.SetMetadata("retained", strconv.FormatBool(m.Retained()))
		msg.SetMetadata("duplicate", strconv.FormatBool(m.Duplicate()))
		select {
		case s.msgCh <- msg:
		default:
			// Channel full: drop oldest by non-blocking receive then push
			select {
			case <-s.msgCh:
			default:
			}
			s.msgCh <- msg
		}
	})

	// OnConnect subscription
	opts.OnConnect = func(c paho.Client) {
		for _, t := range s.topics {
			if token := c.Subscribe(t, s.qos, nil); token.Wait() && token.Error() != nil {
				// Push an error message into the stream as metadata-only entry
				errMsg := message.AcquireMessage()
				errMsg.SetID(fmt.Sprintf("err:%d", time.Now().UnixNano()))
				errMsg.SetOperation(hermod.OpUpdate)
				errMsg.SetMetadata("error", fmt.Sprintf("subscribe %s: %v", t, token.Error()))
				select {
				case s.msgCh <- errMsg:
				default:
				}
			}
		}
	}

	return s, nil
}

func (s *Source) ensureClient() error {
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	if client != nil && client.IsConnectionOpen() {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil && s.client.IsConnectionOpen() {
		return nil
	}
	c := paho.NewClient(s.opts)
	token := c.Connect()
	if ok := token.WaitTimeout(15 * time.Second); !ok {
		return errors.New("mqtt: connect timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: connect failed: %w", err)
	}
	s.client = c
	return nil
}

// Read blocks until a message arrives or context is done. Returns (nil, nil) on context timeout/cancel.
func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	if s.closed {
		return nil, context.Canceled
	}
	if err := s.ensureClient(); err != nil {
		// Allow engine to retry
		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(500 * time.Millisecond):
			return nil, nil
		}
	}
	select {
	case <-ctx.Done():
		return nil, nil
	case msg := <-s.msgCh:
		return msg, nil
	}
}

// Ack is a no-op for MQTT; QoS is handled by the client library.
func (s *Source) Ack(ctx context.Context, msg hermod.Message) error { //nolint:revive
	_ = ctx
	_ = msg
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	_ = ctx
	if err := s.ensureClient(); err != nil {
		return err
	}
	if !s.client.IsConnectionOpen() {
		return errors.New("mqtt: not connected")
	}
	return nil
}

// Sample connects to the broker, subscribes to the configured topics, and waits
// for a single message so the UI can preview the payload and surface its keys as
// available fields for downstream transformation/sink nodes. It uses a dedicated
// connection with a clean session and a fresh client ID, so it never disturbs
// the running ingestion consumer; MQTT delivers by broadcast, so sampling is
// non-destructive and does not consume or skip real data.
func (s *Source) Sample(ctx context.Context, table string) (hermod.Message, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	opts, topics, qos, err := buildClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Buffered so the broker callback never blocks if we have already returned.
	incoming := make(chan paho.Message, 1)
	opts.SetClientID("")       // fresh random ID to avoid clashing with the consumer
	opts.SetCleanSession(true) // never join a durable session when sampling
	opts.AutoReconnect = false
	opts.SetDefaultPublishHandler(func(_ paho.Client, m paho.Message) {
		select {
		case incoming <- m:
		default:
		}
	})
	opts.OnConnect = func(c paho.Client) {
		for _, t := range topics {
			c.Subscribe(t, qos, nil)
		}
	}

	client := paho.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return nil, errors.New("mqtt: connect timeout while sampling")
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("mqtt: connect failed while sampling: %w", err)
	}
	defer client.Disconnect(250)

	select {
	case <-ctx.Done():
		return nil, errors.New("mqtt: no message received within timeout; publish a message to the topic and retry")
	case m := <-incoming:
		return buildSampleMessage(m), nil
	}
}

// buildSampleMessage converts a received MQTT message into a hermod.Message,
// decoding JSON payloads into top-level fields so the workflow editor can list
// them as available fields; non-JSON payloads are preserved under "after".
func buildSampleMessage(m paho.Message) hermod.Message {
	payload := bytes.Clone(m.Payload())
	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("%s:%d", m.Topic(), m.MessageID()))
	msg.SetPayload(payload)

	var jsonData map[string]any
	if err := json.Unmarshal(payload, &jsonData); err == nil {
		for k, v := range jsonData {
			msg.SetData(k, v)
		}
	} else {
		msg.SetAfter(payload)
	}

	msg.SetMetadata("topic", m.Topic())
	msg.SetMetadata("qos", strconv.Itoa(int(m.Qos())))
	msg.SetMetadata("retained", strconv.FormatBool(m.Retained()))
	msg.SetMetadata("sample", "true")
	return msg
}

func (s *Source) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.client != nil {
		s.client.Disconnect(250)
		s.client = nil
	}
	close(s.msgCh)
	return nil
}

func parseCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
