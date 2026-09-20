package schemaregistry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	sr "github.com/gsoultan/hermod/pkg/infra/schemaregistry"
)

const userSchema = `{"type":"record","name":"User","namespace":"io.hermod","fields":[{"name":"id","type":"long"},{"name":"name","type":"string"}]}`

// newStubRegistry answers registrations with a fixed id and counts them.
func newStubRegistry(t *testing.T, id int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var registrations atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registrations.Add(1)
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = fmt.Fprintf(w, `{"id":%d}`, id)
	}))
	t.Cleanup(srv.Close)
	return srv, &registrations
}

// newMsg builds a real DefaultMessage rather than a stub. Avro is strictly
// typed — a schema field declared "long" will not accept a float64 — and
// DefaultMessage's read path normalises some values while its write path
// preserves the Go type. A hand-rolled fake message would hide that
// disagreement, which is exactly the class of bug this formatter can hit.
func newMsg(t *testing.T, data map[string]any) *message.DefaultMessage {
	t.Helper()
	// AcquireMessage, not a struct literal: a zero-value DefaultMessage has a
	// nil data map and SetData panics on it.
	m := message.AcquireMessage()
	for k, v := range data {
		m.SetData(k, v)
	}
	return m
}

// The whole point of this formatter: bytes that a Confluent consumer can read.
// The first five bytes must be the registry header, and the id in them must be
// the one the registry handed back — not a configured constant, and not zero.
func TestFormatFramesWithTheRegistryAssignedID(t *testing.T) {
	srv, _ := newStubRegistry(t, 1234)

	f, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  userSchema,
		Type:    Avro,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := f.Format(newMsg(t, map[string]any{"id": int64(7), "name": "ada"}))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	gotID, payload, err := sr.DecodeFrame(out)
	if err != nil {
		t.Fatalf("output is not Confluent-framed: %v", err)
	}
	if gotID != 1234 {
		t.Errorf("framed schema id = %d, want the registry-assigned 1234", gotID)
	}
	if len(payload) == 0 {
		t.Error("framed payload is empty — the record body was not encoded")
	}
}

// Avro binary is not JSON. If the body comes back as JSON the formatter has
// silently fallen through to the wrong encoder, and the only thing that would
// notice is a foreign consumer in production.
func TestAvroBodyIsBinaryNotJSON(t *testing.T) {
	srv, _ := newStubRegistry(t, 1)

	f, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  userSchema,
		Type:    Avro,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := f.Format(newMsg(t, map[string]any{"id": int64(7), "name": "ada"}))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	_, body, err := sr.DecodeFrame(out)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}

	var probe any
	if json.Unmarshal(body, &probe) == nil {
		t.Errorf("Avro body parsed as JSON (%q) — the record was not Avro-encoded", body)
	}

	// Avro encodes a long as zigzag varint and a string as length-prefixed
	// UTF-8: id=7 is 0x0E, then name="ada" is 0x06 'a' 'd' 'a'.
	want := []byte{0x0E, 0x06, 'a', 'd', 'a'}
	if string(body) != string(want) {
		t.Errorf("Avro body = %#v, want %#v", body, want)
	}
}

func TestJSONSchemaBodyIsJSON(t *testing.T) {
	srv, _ := newStubRegistry(t, 55)

	f, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  `{"type":"object"}`,
		Type:    JSONSchema,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := f.Format(newMsg(t, map[string]any{"name": "ada"}))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	id, body, err := sr.DecodeFrame(out)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if id != 55 {
		t.Errorf("schema id = %d, want 55", id)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("JSON Schema body is not JSON: %v", err)
	}
	if got["name"] != "ada" {
		t.Errorf("body = %v, want name=ada", got)
	}
}

// Registering per message would put an HTTP round trip in front of every
// record. The registry is idempotent, so this is purely about throughput — but
// at a realistic rate it is the difference between working and not.
func TestFormatRegistersTheSchemaOnce(t *testing.T) {
	srv, registrations := newStubRegistry(t, 1)

	f, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  userSchema,
		Type:    Avro,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 10; i++ {
		if _, err := f.Format(newMsg(t, map[string]any{"id": int64(i), "name": "ada"})); err != nil {
			t.Fatalf("Format #%d: %v", i, err)
		}
	}
	if n := registrations.Load(); n != 1 {
		t.Errorf("registry received %d registrations for 10 messages, want 1", n)
	}
}

// A message missing a field the schema declares required cannot be encoded.
// The failure must name the message and be a normal error — a sink turns that
// into a dead-letter, where a panic takes the worker down with it.
func TestFormatReportsAMessageThatDoesNotMatchTheSchema(t *testing.T) {
	srv, _ := newStubRegistry(t, 1)

	f, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  userSchema,
		Type:    Avro,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// No "name" field, which the schema declares as a required string.
	_, err = f.Format(newMsg(t, map[string]any{"id": int64(1)}))
	if err == nil {
		t.Fatal("Format accepted a message missing a required field, want an error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "encode") &&
		!strings.Contains(strings.ToLower(err.Error()), "avro") {
		t.Errorf("error %q does not say the encode failed", err)
	}
}

func TestFormatRejectsANilMessage(t *testing.T) {
	srv, _ := newStubRegistry(t, 1)
	f, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  userSchema,
		Type:    Avro,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := f.Format(nil); err == nil {
		t.Error("Format(nil) succeeded, want an error")
	}
}

// Protobuf's registry framing carries a message-index array after the schema
// id that this formatter does not write. Claiming support and emitting an
// Avro-shaped frame would produce bytes no Protobuf consumer can read, so it
// must refuse at construction rather than at the first message.
func TestNewRefusesProtobufRatherThanEmittingAnUnreadableFrame(t *testing.T) {
	srv, _ := newStubRegistry(t, 1)
	_, err := New(Config{
		Client:  mustClient(t, srv.URL),
		Subject: "users-value",
		Schema:  userSchema,
		Type:    Protobuf,
	})
	if err == nil {
		t.Fatal("New accepted Protobuf, want a refusal until message-index framing is implemented")
	}
	if !strings.Contains(err.Error(), "message-index") {
		t.Errorf("error %q does not say why Protobuf is refused", err)
	}
}

func TestNewValidatesItsConfig(t *testing.T) {
	srv, _ := newStubRegistry(t, 1)
	good := mustClient(t, srv.URL)

	tests := []struct {
		name string
		cfg  Config
	}{
		{"no client", Config{Subject: "s", Schema: userSchema, Type: Avro}},
		{"no subject", Config{Client: good, Schema: userSchema, Type: Avro}},
		{"no schema", Config{Client: good, Subject: "s", Type: Avro}},
		{"unparseable avro schema", Config{Client: good, Subject: "s", Schema: "{{{", Type: Avro}},
		{"unknown type", Config{Client: good, Subject: "s", Schema: userSchema, Type: Type("xml")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Errorf("New(%s) succeeded, want an error", tc.name)
			}
		})
	}
}

func mustClient(t *testing.T, baseURL string) *sr.Client {
	t.Helper()
	c, err := sr.NewClient(sr.Config{BaseURL: baseURL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}
