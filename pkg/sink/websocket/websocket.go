package websocket

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	hermod "github.com/user/hermod"
)

// Sink implements a simple WebSocket sink. It dials out to a ws/wss URL and
// writes one text frame per message. The frame payload is produced by the
// configured Formatter (defaults to JSON when wired by factory).
// Optional application-level ACK can be enabled via RequireAck.
type Sink struct {
	// Runtime
	mu   sync.Mutex
	conn *websocket.Conn

	// Config
	url               string
	headers           map[string]string
	subprotocols      []string
	connectTimeout    time.Duration
	writeTimeout      time.Duration
	heartbeatInterval time.Duration
	requireAck        bool
	tlsCfg            *tls.Config
	pinSHA256         string

	// Dependencies
	dialer    websocket.Dialer
	formatter hermod.Formatter
}

// New creates a new WebSocket sink.
func New(url string, headers map[string]string, subprotocols []string, connectTimeout, writeTimeout, heartbeatInterval time.Duration, requireAck bool, fmttr hermod.Formatter) *Sink {
	d := websocket.Dialer{Subprotocols: subprotocols}
	return &Sink{
		url:               url,
		headers:           headers,
		subprotocols:      subprotocols,
		connectTimeout:    connectTimeout,
		writeTimeout:      writeTimeout,
		heartbeatInterval: heartbeatInterval,
		requireAck:        requireAck,
		dialer:            d,
		formatter:         fmttr,
	}
}

func (s *Sink) ensureConn(ctx context.Context) error {
	if s.conn != nil {
		return nil
	}
	hdr := http.Header{}
	for k, v := range s.headers {
		hdr.Set(k, v)
	}
	cctx, cancel := context.WithTimeout(ctx, s.connectTimeout)
	defer cancel()
	if s.tlsCfg != nil {
		s.dialer.TLSClientConfig = s.tlsCfg
	}
	c, _, err := s.dialer.DialContext(cctx, s.url, hdr)
	if err != nil {
		return err
	}
	if s.pinSHA256 != "" {
		if tc, ok := c.UnderlyingConn().(*tls.Conn); ok {
			st := tc.ConnectionState()
			if len(st.PeerCertificates) > 0 {
				sum := sha256.Sum256(st.PeerCertificates[0].Raw)
				got := base64.StdEncoding.EncodeToString(sum[:])
				if got != s.pinSHA256 {
					_ = c.Close()
					return errors.New("websocket tls pin mismatch")
				}
			}
		}
	}
	s.conn = c
	return nil
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureConn(ctx); err != nil {
		return err
	}
	// Prepare payload
	var payload []byte
	var err error
	if s.formatter != nil {
		payload, err = s.formatter.Format(msg)
		if err != nil {
			return err
		}
	} else {
		// fallback: minimal JSON envelope
		env := map[string]any{
			"id":       msg.ID(),
			"op":       string(msg.Operation()),
			"table":    msg.Table(),
			"schema":   msg.Schema(),
			"metadata": msg.Metadata(),
		}
		// Prefer payload over before/after when set
		if p := msg.Payload(); len(p) > 0 {
			// Try to keep JSON as-is if it's JSON, else quote as string
			var js any
			if json.Unmarshal(p, &js) == nil {
				env["payload"] = json.RawMessage(p)
			} else {
				env["payload_base64"] = p // avoid corruption; UI/peer can decode as needed
			}
		}
		payload, _ = json.Marshal(env)
	}

	deadline := time.Now().Add(s.writeTimeout)
	if s.heartbeatInterval > 0 {
		// Light liveness check
		_ = s.conn.WriteControl(websocket.PingMessage, []byte("ping"), deadline)
	}

	// Stricter deadline for data frame
	_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
	if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		_ = s.conn.Close()
		s.conn = nil
		return err
	}

	if s.requireAck {
		// Wait for an ACK frame: {"ack":"<id>","ok":true}
		_ = s.conn.SetReadDeadline(time.Now().Add(s.writeTimeout))
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return err
		}
		type ack struct {
			Ack   string `json:"ack"`
			Ok    bool   `json:"ok"`
			Error string `json:"error"`
		}
		var a ack
		if err := json.Unmarshal(data, &a); err != nil {
			return err
		}
		if !a.Ok || strings.TrimSpace(a.Ack) != msg.ID() {
			if a.Error != "" {
				return errors.New(a.Error)
			}
			return errors.New("websocket sink: ack failed or mismatched id")
		}
	}
	return nil
}

func (s *Sink) Ping(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		if err := s.ensureConn(ctx); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(s.writeTimeout)
	return s.conn.WriteControl(websocket.PingMessage, []byte("ping"), deadline)
}

func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		return err
	}
	return nil
}

// SetTLSConfig allows configuring TLS options and optional cert pinning.
func (s *Sink) SetTLSConfig(cfg *tls.Config, pinSHA256 string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tlsCfg = cfg
	s.pinSHA256 = pinSHA256
}
