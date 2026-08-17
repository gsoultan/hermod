//go:build integration

package mqtt

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// The MQTT source, against a real broker.
//
// MQTT was the last Beta source with infrastructure reachable from a
// workstation and no data-path test: the earlier fixes (the silent
// drop-oldest, the connection leak, the panic on close) were all proven
// through the client callback surface, never through a broker. Mosquitto runs
// natively on arm64 under Apple's container runtime, so "unreachable" was
// never true of this one — it was just never done.
//
// Run with:
//
//	HERMOD_INTEGRATION=1 MQTT_BROKER=tcp://127.0.0.1:1883 \
//	go test -tags=integration ./pkg/comm/source/mqtt/
func requireBroker(t *testing.T) string {
	t.Helper()
	broker := os.Getenv("MQTT_BROKER")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || broker == "" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			t.Fatalf("HERMOD_INTEGRATION=%q MQTT_BROKER=%q in CI, where a broker is "+
				"started for exactly this: a quietly skipped suite reads as a green one",
				os.Getenv("HERMOD_INTEGRATION"), broker)
		}
		t.Skip("integration: set HERMOD_INTEGRATION=1 and MQTT_BROKER to run")
	}
	return broker
}

// readMsg drives Read under its engine contract: (nil, nil) means "nothing
// yet, poll again" — the engine retries, so the test must too. Treating that
// pair as data dereferences a nil interface, which this sandbox turns into a
// silent hang rather than a panic.
func readMsg(t *testing.T, ctx context.Context, src *Source) (msg interface {
	Payload() []byte
	Metadata() map[string]string
}) {
	t.Helper()
	for {
		if ctx.Err() != nil {
			t.Fatal("timed out waiting for a message from the broker")
		}
		m, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if m != nil {
			return m
		}
	}
}

func publish(t *testing.T, broker, topic string, payloads ...string) {
	t.Helper()
	opts := paho.NewClientOptions().AddBroker(broker).SetClientID(
		fmt.Sprintf("hermod-it-pub-%d", time.Now().UnixNano()))
	client := paho.NewClient(opts)
	tok := client.Connect()
	if !tok.WaitTimeout(10*time.Second) || tok.Error() != nil {
		t.Fatalf("publisher cannot reach the broker at %s: %v", broker, tok.Error())
	}
	defer client.Disconnect(250)
	for _, p := range payloads {
		pt := client.Publish(topic, 1, false, p)
		if !pt.WaitTimeout(10*time.Second) || pt.Error() != nil {
			t.Fatalf("publish: %v", pt.Error())
		}
	}
}

// A published message comes out of Read with its payload and topic intact.
func TestAPublishedMessageComesOutOfRead(t *testing.T) {
	broker := requireBroker(t)
	topic := fmt.Sprintf("hermod/it/%d", time.Now().UnixNano())

	src, err := NewSource(map[string]string{
		"broker_url": broker,
		"topics":     topic,
		"qos":        "1",
		"client_id":  fmt.Sprintf("hermod-it-sub-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()

	// First Read connects and subscribes; publish after the subscription is
	// live, from a goroutine, since Read blocks.
	go func() {
		time.Sleep(2 * time.Second)
		publish(t, broker, topic, `{"n":1}`)
	}()

	msg := readMsg(t, ctx, src)
	if string(msg.Payload()) != `{"n":1}` {
		t.Errorf("payload arrived as %q, want {\"n\":1}", msg.Payload())
	}
	if got := msg.Metadata()["topic"]; got != topic {
		t.Errorf("topic metadata is %q, want %q", got, topic)
	}
}

// Every message in a burst arrives — the drop-oldest bug this source used to
// have would eat the head of the burst silently.
func TestABurstArrivesWhole(t *testing.T) {
	broker := requireBroker(t)
	topic := fmt.Sprintf("hermod/it/burst/%d", time.Now().UnixNano())

	src, err := NewSource(map[string]string{
		"broker_url": broker,
		"topics":     topic,
		"qos":        "1",
		"client_id":  fmt.Sprintf("hermod-it-burst-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	const n = 200
	go func() {
		time.Sleep(2 * time.Second)
		payloads := make([]string, n)
		for i := range payloads {
			payloads[i] = fmt.Sprintf(`{"i":%d}`, i)
		}
		publish(t, broker, topic, payloads...)
	}()

	got := make(map[string]bool, n)
	for len(got) < n {
		msg := readMsg(t, ctx, src)
		got[string(msg.Payload())] = true
	}
}
