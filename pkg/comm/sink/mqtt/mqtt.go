package mqtt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strconv"
	"strings"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/user/hermod"
)

// Sink implements hermod.Sink for MQTT brokers using Eclipse Paho.
type Sink struct {
	client    paho.Client
	opts      *paho.ClientOptions
	topic     string
	qos       byte
	retain    bool
	formatter hermod.Formatter
}

// New creates a new MQTT sink. Expected config keys:
// - broker_url (or url)
// - topic
// - client_id, username, password (optional)
// - qos: 0|1|2 (default 1)
// - retain: true|false (default false)
// - clean_session: true|false (default true)
// - keepalive: duration seconds (default 30s)
// - tls_insecure_skip_verify: true|false
func New(cfg map[string]string, formatter hermod.Formatter) (*Sink, error) {
	brokerURL := strings.TrimSpace(cfg["broker_url"])
	if brokerURL == "" {
		brokerURL = strings.TrimSpace(cfg["url"])
	}
	if brokerURL == "" {
		return nil, fmt.Errorf("mqtt: broker_url is required")
	}
	topic := strings.TrimSpace(cfg["topic"])
	if topic == "" {
		return nil, fmt.Errorf("mqtt: topic is required")
	}

	opts := paho.NewClientOptions().AddBroker(brokerURL)
	if cid := strings.TrimSpace(cfg["client_id"]); cid != "" {
		opts.SetClientID(cid)
	} else {
		opts.SetClientID("")
	}
	if u := strings.TrimSpace(cfg["username"]); u != "" {
		opts.SetUsername(u)
		opts.SetPassword(cfg["password"]) // may be empty
	}

	cleanSession := true
	if v := strings.TrimSpace(cfg["clean_session"]); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cleanSession = b
		}
	}
	opts.SetCleanSession(cleanSession)

	keepAlive := 30 * time.Second
	if v := strings.TrimSpace(cfg["keepalive"]); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			keepAlive = d
		} else if n, err := strconv.Atoi(v); err == nil {
			keepAlive = time.Duration(n) * time.Second
		}
	}
	opts.SetKeepAlive(keepAlive)

	// TLS if broker scheme is secure
	if strings.HasPrefix(brokerURL, "ssl://") || strings.HasPrefix(brokerURL, "tls://") || strings.HasPrefix(brokerURL, "wss://") {
		tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
		if roots, err := x509.SystemCertPool(); err == nil && roots != nil {
			tlsCfg.RootCAs = roots
		}
		if v := strings.TrimSpace(cfg["tls_insecure_skip_verify"]); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				tlsCfg.InsecureSkipVerify = b
			}
		}
		opts.SetTLSConfig(tlsCfg)
	}

	qos := byte(1)
	if v := strings.TrimSpace(cfg["qos"]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 2 {
			qos = byte(n)
		}
	}

	retain := false
	if v := strings.TrimSpace(cfg["retain"]); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			retain = b
		}
	}

	s := &Sink{
		opts:      opts,
		topic:     topic,
		qos:       qos,
		retain:    retain,
		formatter: formatter,
	}
	return s, nil
}

func (s *Sink) ensureClient(ctx context.Context) error {
	if s.client != nil && s.client.IsConnectionOpen() {
		return nil
	}
	c := paho.NewClient(s.opts)
	token := c.Connect()
	if !token.WaitTimeout(15 * time.Second) {
		return fmt.Errorf("mqtt: connect timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: connect failed: %w", err)
	}
	s.client = c
	return nil
}

func (s *Sink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureClient(ctx); err != nil {
		return err
	}

	var data []byte
	var err error
	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
		if err != nil {
			return fmt.Errorf("mqtt: format error: %w", err)
		}
	} else {
		data = msg.Payload()
	}

	token := s.client.Publish(s.topic, s.qos, s.retain, data)
	// Respect context if provided
	done := make(chan struct{})
	go func() {
		token.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		if err := token.Error(); err != nil {
			return fmt.Errorf("mqtt: publish failed: %w", err)
		}
	}
	return nil
}

func (s *Sink) Ping(ctx context.Context) error {
	return s.ensureClient(ctx)
}

func (s *Sink) Close() error {
	if s.client != nil {
		s.client.Disconnect(250)
		s.client = nil
	}
	return nil
}
