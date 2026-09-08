//go:build integration

package factory_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"

	amqp "github.com/rabbitmq/amqp091-go"
)

// The wizard's connection step writes these keys; nothing writes `url` once a
// host is present. This drives the same map through the real factory to a real
// broker, so the UI gate and the running connector are checked against one
// another instead of against my reading of them.
func rabbitEnvConfig(t *testing.T) hermod.StringMap {
	t.Helper()
	if os.Getenv("HERMOD_INTEGRATION") != "1" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 to run")
	}
	host := os.Getenv("RABBITMQ_HOST")
	if host == "" {
		t.Skip("integration: set RABBITMQ_HOST to run")
	}
	return hermod.StringMap{
		"host":       host,
		"port":       os.Getenv("RABBITMQ_PORT"),
		"username":   os.Getenv("RABBITMQ_USER"),
		"password":   os.Getenv("RABBITMQ_PASS"),
		"dbname":     os.Getenv("RABBITMQ_VHOST"),
		"queue_name": os.Getenv("RABBITMQ_QUEUE"),
		"use_ssl":    "false",
	}
}

// A password containing '@' is a URL delimiter. If it reached the AMQP URL raw,
// the host would parse as everything after the last '@' and the connection
// would fail in a way that looks like a wrong hostname.
func TestRabbitMQQueue_ConnStringEscapesCredentials(t *testing.T) {
	cfg := rabbitEnvConfig(t)
	got := factory.BuildConnectionString(cfg, "rabbitmq_queue")
	t.Logf("connection string: %s", got)

	u, err := amqp.ParseURI(got)
	if err != nil {
		t.Fatalf("factory produced a URL the AMQP client cannot parse: %v", err)
	}
	if u.Host != cfg["host"] {
		t.Errorf("host = %q, want %q — credentials leaked into the authority", u.Host, cfg["host"])
	}
	if u.Password != cfg["password"] {
		t.Errorf("password did not survive the round trip: got %q", u.Password)
	}
	if u.Vhost != cfg["dbname"] {
		t.Errorf("vhost = %q, want %q", u.Vhost, cfg["dbname"])
	}
}

// Test Connection, on the real broker, from the real config map.
func TestRabbitMQQueue_LivePing(t *testing.T) {
	cfg := rabbitEnvConfig(t)
	src, err := factory.CreateSource(factory.SourceConfig{
		ID: "live-check", Type: "rabbitmq_queue", Config: cfg,
	})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	defer src.Close()

	pinger, ok := src.(interface{ Ping(ctx context.Context) error })
	if !ok {
		t.Fatal("source does not expose Ping — Test Connection could not work")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := pinger.Ping(ctx); err != nil {
		t.Fatalf("Ping failed against the live broker: %v", err)
	}
	t.Log("Ping OK — Test Connection succeeds with the config the wizard writes")
}

// The step the wizard leads to: actually consume a message.
func TestRabbitMQQueue_LiveReadAck(t *testing.T) {
	cfg := rabbitEnvConfig(t)
	connStr := factory.BuildConnectionString(cfg, "rabbitmq_queue")
	queue := cfg["queue_name"]

	src, err := factory.CreateSource(factory.SourceConfig{
		ID: "live-check", Type: "rabbitmq_queue", Config: cfg,
	})
	if err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	defer src.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	body := []byte(`{"hermod":"wizard-live-check","n":1}`)
	conn, err := amqp.Dial(connStr)
	if err != nil {
		t.Fatalf("publisher dial: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("publisher channel: %v", err)
	}
	defer ch.Close()
	if err := ch.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{
		ContentType: "application/json", Body: body,
	}); err != nil {
		t.Fatalf("publish to %s: %v", queue, err)
	}

	msg, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	t.Logf("read %d bytes: %s", len(msg.Payload()), string(msg.Payload()))
	if err := src.Ack(ctx, msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	t.Log("Read+Ack OK — the source drains the queue it was configured for")
}
