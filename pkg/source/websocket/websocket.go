package websocket

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	hermod "github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// Source is a client-mode WebSocket source that dials a ws/wss URL and
// produces one message per received text frame. The frame is expected to be a
// JSON envelope with optional fields; raw payload is placed into Message.Payload.
type Source struct {
	// Config
	url               string
	headers           map[string]string
	subprotocols      []string
	connectTimeout    time.Duration
	readTimeout       time.Duration
	heartbeatInterval time.Duration
	reconnectBase     time.Duration
	reconnectMax      time.Duration
	maxMessageBytes   int64
	tlsCfg            *tls.Config
	pinSHA256         string // base64-encoded SHA256 of leaf cert

	// Runtime
	mu   sync.Mutex
	dlr  websocket.Dialer
	conn *websocket.Conn
	out  chan hermod.Message
	quit chan struct{}
}

type envelope struct {
	ID       string            `json:"id"`
	Op       string            `json:"op"`
	Table    string            `json:"table,omitempty"`
	Schema   string            `json:"schema,omitempty"`
	Payload  json.RawMessage   `json:"payload,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

func New(url string, headers map[string]string, subprotocols []string, connectTimeout, readTimeout, heartbeatInterval, reconnectBase, reconnectMax time.Duration, maxMessageBytes int64) *Source {
	if reconnectBase <= 0 {
		reconnectBase = time.Second
	}
	if reconnectMax <= 0 {
		reconnectMax = 30 * time.Second
	}
	s := &Source{
		url:               url,
		headers:           headers,
		subprotocols:      subprotocols,
		connectTimeout:    connectTimeout,
		readTimeout:       readTimeout,
		heartbeatInterval: heartbeatInterval,
		reconnectBase:     reconnectBase,
		reconnectMax:      reconnectMax,
		maxMessageBytes:   maxMessageBytes,
		dlr:               websocket.Dialer{Subprotocols: subprotocols},
		out:               make(chan hermod.Message, 1024),
		quit:              make(chan struct{}),
	}
	return s
}

// SetTLSConfig allows providing a custom TLS configuration (custom roots, SNI, etc.).
func (s *Source) SetTLSConfig(cfg *tls.Config, pinSHA256 string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tlsCfg = cfg
	s.pinSHA256 = pinSHA256
}

func (s *Source) connect(ctx context.Context) error {
	hdr := http.Header{}
	for k, v := range s.headers {
		hdr.Set(k, v)
	}
	cctx, cancel := context.WithTimeout(ctx, s.connectTimeout)
	defer cancel()
	if s.tlsCfg != nil {
		s.dlr.TLSClientConfig = s.tlsCfg
	}
	c, _, err := s.dlr.DialContext(cctx, s.url, hdr)
	if err != nil {
		return err
	}
	// Optional pin check
	if s.pinSHA256 != "" {
		if tc, ok := c.UnderlyingConn().(*tls.Conn); ok {
			state := tc.ConnectionState()
			if len(state.PeerCertificates) > 0 {
				sum := sha256.Sum256(state.PeerCertificates[0].Raw)
				got := base64.StdEncoding.EncodeToString(sum[:])
				if got != s.pinSHA256 {
					_ = c.Close()
					return errors.New("websocket tls pin mismatch")
				}
			}
		}
	}
	s.conn = c
	if s.maxMessageBytes > 0 {
		s.conn.SetReadLimit(s.maxMessageBytes)
	}
	if s.heartbeatInterval > 0 {
		_ = s.conn.SetReadDeadline(time.Now().Add(2*s.heartbeatInterval + 10*time.Second))
		s.conn.SetPongHandler(func(string) error {
			return s.conn.SetReadDeadline(time.Now().Add(2*s.heartbeatInterval + 10*time.Second))
		})
	}
	return nil
}

func (s *Source) loop(ctx context.Context) {
	backoff := s.reconnectBase
	for {
		select {
		case <-s.quit:
			return
		default:
		}

		s.mu.Lock()
		if s.conn == nil {
			if err := s.connect(ctx); err != nil {
				s.mu.Unlock()
				// Exponential backoff with jitter (50%)
				jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
				time.Sleep(backoff + jitter)
				if backoff < s.reconnectMax {
					backoff *= 2
					if backoff > s.reconnectMax {
						backoff = s.reconnectMax
					}
				}
				continue
			}
			backoff = s.reconnectBase
		}
		c := s.conn
		s.mu.Unlock()

		if s.heartbeatInterval > 0 {
			_ = c.SetReadDeadline(time.Now().Add(2*s.heartbeatInterval + 10*time.Second))
		} else if s.readTimeout > 0 {
			_ = c.SetReadDeadline(time.Now().Add(s.readTimeout))
		}

		msgType, data, err := c.ReadMessage()
		if err != nil {
			s.mu.Lock()
			_ = c.Close()
			s.conn = nil
			s.mu.Unlock()
			continue
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		var env envelope
		if err := json.Unmarshal(data, &env); err != nil {
			// Not an envelope: treat entire frame as payload
			m := message.AcquireMessage()
			m.SetPayload(data)
			select {
			case s.out <- m:
			case <-ctx.Done():
				return
			}
			continue
		}
		m := message.AcquireMessage()
		if env.ID != "" {
			// we can set as metadata; DefaultMessage has internal ID generator
			m.SetMetadata("ws_id", env.ID)
		}
		if len(env.Payload) > 0 {
			m.SetPayload(env.Payload)
		}
		if env.Op != "" {
			// optional operation mapping
			switch env.Op {
			case string(hermod.OpCreate):
				m.SetOperation(hermod.OpCreate)
			case string(hermod.OpUpdate):
				m.SetOperation(hermod.OpUpdate)
			case string(hermod.OpDelete):
				m.SetOperation(hermod.OpDelete)
			case string(hermod.OpSnapshot):
				m.SetOperation(hermod.OpSnapshot)
			}
		}
		if env.Table != "" {
			m.SetTable(env.Table)
		}
		if env.Schema != "" {
			m.SetSchema(env.Schema)
		}
		for k, v := range env.Metadata {
			m.SetMetadata(k, v)
		}
		select {
		case s.out <- m:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	// Start loop on first Read
	s.mu.Lock()
	if s.out == nil {
		s.out = make(chan hermod.Message, 1024)
	}
	if s.quit == nil {
		s.quit = make(chan struct{})
	}
	s.mu.Unlock()

	// Ensure loop is running
	go s.loop(ctx)

	select {
	case m := <-s.out:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Ack is a no-op for client-mode WS source.
func (s *Source) Ack(ctx context.Context, msg hermod.Message) error { return nil }

func (s *Source) Ping(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	deadline := time.Now().Add(5 * time.Second)
	return s.conn.WriteControl(websocket.PingMessage, []byte("ping"), deadline)
}

func (s *Source) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.quit != nil {
		close(s.quit)
		s.quit = nil
	}
	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		return err
	}
	return nil
}
