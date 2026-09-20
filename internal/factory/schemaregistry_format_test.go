package factory

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hamba/avro/v2"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sr "github.com/gsoultan/hermod/pkg/infra/schemaregistry"
)

const srTestSchema = `{"type":"record","name":"User","fields":[{"name":"id","type":"long"}]}`

// A formatter that can only be constructed from Go is not a feature an operator
// has. Every one of these starts from the stored config map a workflow actually
// persists, because three shipped bugs in this repository had full coverage of
// the parts and none of the assembly.
func TestSchemaRegistryFormatIsReachableFromStoredConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = fmt.Fprint(w, `{"id":99}`)
	}))
	defer srv.Close()

	f, err := buildFormatter(hermod.StringMap{
		"format":                   "schema_registry",
		"schema_registry_url":      srv.URL,
		"schema_registry_subject":  "users-value",
		"schema_registry_schema":   srTestSchema,
		"schema_registry_type":     "AVRO",
		"schema_registry_username": "key",
		"schema_registry_password": "secret",
	})
	if err != nil {
		t.Fatalf("buildFormatter: %v", err)
	}
	if f == nil {
		t.Fatal("buildFormatter returned no formatter for format=schema_registry")
	}

	msg := message.AcquireMessage()
	msg.SetData("id", int64(7))

	out, err := f.Format(msg)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}

	id, body, err := sr.DecodeFrame(out)
	if err != nil {
		t.Fatalf("sink output is not Confluent-framed: %v", err)
	}
	if id != 99 {
		t.Errorf("framed schema id = %d, want the registry-assigned 99", id)
	}
	if len(body) == 0 {
		t.Error("framed body is empty")
	}
}

// Misconfiguration must be refused when the workflow is built, not on the first
// message. A sink that starts and then fails every record looks like a broken
// destination rather than a typo.
func TestSchemaRegistryFormatRefusesIncompleteConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     hermod.StringMap
		wantMsg string
	}{
		{
			name:    "no url",
			cfg:     hermod.StringMap{"format": "schema_registry", "schema_registry_subject": "s", "schema_registry_schema": srTestSchema},
			wantMsg: "schema_registry_url",
		},
		{
			name:    "no subject",
			cfg:     hermod.StringMap{"format": "schema_registry", "schema_registry_url": "http://x", "schema_registry_schema": srTestSchema},
			wantMsg: "schema_registry_subject",
		},
		{
			name:    "no schema",
			cfg:     hermod.StringMap{"format": "schema_registry", "schema_registry_url": "http://x", "schema_registry_subject": "s"},
			wantMsg: "schema_registry_schema",
		},
		{
			name: "unparseable schema",
			cfg: hermod.StringMap{"format": "schema_registry", "schema_registry_url": "http://x",
				"schema_registry_subject": "s", "schema_registry_schema": "{{{"},
			wantMsg: "Avro",
		},
		{
			name: "protobuf, whose framing this does not write",
			cfg: hermod.StringMap{"format": "schema_registry", "schema_registry_url": "http://x",
				"schema_registry_subject": "s", "schema_registry_schema": srTestSchema, "schema_registry_type": "PROTOBUF"},
			wantMsg: "message-index",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildFormatter(tc.cfg)
			if err == nil {
				t.Fatalf("buildFormatter accepted %s, want an error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not mention %q — an operator cannot act on it", err, tc.wantMsg)
			}
		})
	}
}

// The formats that existed before this change must behave exactly as they did.
func TestBuildFormatterPreservesExistingFormats(t *testing.T) {
	tests := []struct {
		format string
		want   bool // a formatter is expected
	}{
		{"json", true},
		{"cdc", true},
		{"payload", true},
		{"", false}, // no format set means raw payload bytes; must stay nil
		{"unknown", false},
	}

	for _, tc := range tests {
		t.Run("format="+tc.format, func(t *testing.T) {
			f, err := buildFormatter(hermod.StringMap{"format": tc.format})
			if err != nil {
				t.Fatalf("buildFormatter(%q): %v", tc.format, err)
			}
			if got := f != nil; got != tc.want {
				t.Errorf("buildFormatter(%q) returned formatter=%v, want %v", tc.format, got, tc.want)
			}
		})
	}
}

// The credentials belong in the request, not in the workflow's logs. This is
// the same seam the secret: prefix resolves through, so the value reaching here
// may be a real secret.
func TestSchemaRegistryFormatSendsItsCredentials(t *testing.T) {
	var user, pass string
	var ok bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok = r.BasicAuth()
		_, _ = fmt.Fprint(w, `{"id":1}`)
	}))
	defer srv.Close()

	f, err := buildFormatter(hermod.StringMap{
		"format":                   "schema_registry",
		"schema_registry_url":      srv.URL,
		"schema_registry_subject":  "users-value",
		"schema_registry_schema":   srTestSchema,
		"schema_registry_username": "key",
		"schema_registry_password": "secret",
	})
	if err != nil {
		t.Fatalf("buildFormatter: %v", err)
	}

	msg := message.AcquireMessage()
	msg.SetData("id", int64(1))
	if _, err := f.Format(msg); err != nil {
		t.Fatalf("Format: %v", err)
	}

	if !ok {
		t.Fatal("registry request carried no Authorization header")
	}
	if user != "key" || pass != "secret" {
		t.Errorf("basic auth = %q/%q, want key/secret", user, pass)
	}
}

// The source side of the same assembly question: a Kafka source configured for
// a registry topic must actually come back with a decoder attached, or the
// stored config is a setting that reaches nothing.
func TestKafkaSourceGetsADecoderFromStoredConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = fmt.Fprintf(w, `{"schema":%q}`, srTestSchema)
	}))
	defer srv.Close()

	dec, err := buildRecordDecoder(hermod.StringMap{
		"format":              "schema_registry",
		"schema_registry_url": srv.URL,
	})
	if err != nil {
		t.Fatalf("buildRecordDecoder: %v", err)
	}
	if dec == nil {
		t.Fatal("no decoder built for format=schema_registry")
	}

	schema, err := avro.Parse(srTestSchema)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body, err := avro.Marshal(schema, map[string]any{"id": int64(5)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := dec.Decode(context.Background(), sr.EncodeFrame(1, body))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["id"] != int64(5) {
		t.Errorf("id = %#v, want int64(5)", got["id"])
	}
}

// A source with no registry format configured must not get a decoder: that
// would turn every existing JSON Kafka source into an error on the first
// record.
func TestNonRegistrySourceGetsNoDecoder(t *testing.T) {
	for _, format := range []string{"", "json", "cdc", "payload"} {
		dec, err := buildRecordDecoder(hermod.StringMap{"format": format})
		if err != nil {
			t.Fatalf("buildRecordDecoder(%q): %v", format, err)
		}
		if dec != nil {
			t.Errorf("format=%q produced a decoder; existing sources would start failing", format)
		}
	}
}

func TestSourceDecoderRequiresARegistryURL(t *testing.T) {
	if _, err := buildRecordDecoder(hermod.StringMap{"format": "schema_registry"}); err == nil {
		t.Error("buildRecordDecoder accepted format=schema_registry with no URL, want an error")
	}
}
