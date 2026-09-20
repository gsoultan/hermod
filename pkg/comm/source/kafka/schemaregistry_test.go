package kafka

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hamba/avro/v2"

	sr "github.com/gsoultan/hermod/pkg/infra/schemaregistry"
)

const kafkaUserSchema = `{"type":"record","name":"User","fields":[
	{"name":"id","type":"long"},{"name":"name","type":"string"}]}`

func registryFor(t *testing.T, schema string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = fmt.Fprintf(w, `{"schema":%q}`, schema)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func framed(t *testing.T, id uint32, schema string, v map[string]any) []byte {
	t.Helper()
	s, err := avro.Parse(schema)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body, err := avro.Marshal(s, v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return sr.EncodeFrame(id, body)
}

// Without a decoder the source's JSON attempt fails on Avro binary and the
// bytes land unparsed in the payload. That is the behaviour this wiring
// replaces, and asserting it first is what makes the next test meaningful.
func TestWithoutADecoderAvroBytesStayOpaque(t *testing.T) {
	s := &KafkaSource{}
	msg, err := s.buildMessage(context.Background(), framed(t, 1, kafkaUserSchema,
		map[string]any{"id": int64(7), "name": "ada"}), "t", 0, 0)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if v, ok := msg.Data()["id"]; ok {
		t.Errorf("id = %#v decoded without a decoder configured; the test below proves nothing", v)
	}
}

// The assembly check: a decoder configured on the source has to reach the
// per-record path, or every bound and every test in avrodecode is decoration.
func TestSourceDecodesAFramedRecordWhenConfigured(t *testing.T) {
	srv := registryFor(t, kafkaUserSchema)
	client, err := sr.NewClient(sr.Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	s := &KafkaSource{}
	s.SetDecoder(sr.NewDecoder(client))

	msg, err := s.buildMessage(context.Background(), framed(t, 1, kafkaUserSchema,
		map[string]any{"id": int64(7), "name": "ada"}), "users", 3, 99)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}

	data := msg.Data()
	if data["id"] != int64(7) {
		t.Errorf("id = %#v, want int64(7)", data["id"])
	}
	if data["name"] != "ada" {
		t.Errorf("name = %#v, want ada", data["name"])
	}

	// The Kafka coordinates must survive decoding — they are what the offset
	// and the trace are keyed on.
	if got := msg.Metadata()["kafka_topic"]; got != "users" {
		t.Errorf("kafka_topic = %q, want users", got)
	}
	if got := msg.Metadata()["kafka_offset"]; got != "99" {
		t.Errorf("kafka_offset = %q, want 99", got)
	}
}

// A record the decoder cannot read is a poison message. It must be reported as
// an error rather than silently delivered with an empty or partial body, which
// is how undecodable data reaches a sink looking like a legitimate empty row.
func TestUndecodableRecordIsAnErrorNotAnEmptyMessage(t *testing.T) {
	srv := registryFor(t, kafkaUserSchema)
	client, _ := sr.NewClient(sr.Config{BaseURL: srv.URL})

	s := &KafkaSource{}
	s.SetDecoder(sr.NewDecoder(client))

	// Framed, valid schema id, truncated body.
	_, err := s.buildMessage(context.Background(), sr.EncodeFrame(1, []byte{0x0E}), "t", 0, 0)
	if err == nil {
		t.Fatal("buildMessage accepted a truncated Avro body, want an error")
	}
}

// With a decoder configured, a plain-JSON record is a misconfiguration rather
// than something to silently fall back on: the topic is not what the operator
// said it was, and quietly accepting it hides that until the data is wrong.
func TestUnframedRecordIsRefusedWhenADecoderIsConfigured(t *testing.T) {
	srv := registryFor(t, kafkaUserSchema)
	client, _ := sr.NewClient(sr.Config{BaseURL: srv.URL})

	s := &KafkaSource{}
	s.SetDecoder(sr.NewDecoder(client))

	if _, err := s.buildMessage(context.Background(), []byte(`{"id":1}`), "t", 0, 0); err == nil {
		t.Error("buildMessage accepted plain JSON on a registry-configured source, want an error")
	}
}
