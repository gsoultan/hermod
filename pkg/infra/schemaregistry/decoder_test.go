package schemaregistry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hamba/avro/v2"
)

const decUserSchema = `{"type":"record","name":"User","fields":[
	{"name":"id","type":"long"},{"name":"name","type":"string"}]}`

// registryServing answers /schemas/ids/<id> with schema and counts the calls.
func registryServing(t *testing.T, schema string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", contentType)
		_, _ = fmt.Fprintf(w, `{"schema":%q}`, schema)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func framedRecord(t *testing.T, id uint32, schema string, v map[string]any) []byte {
	t.Helper()
	s, err := avro.Parse(schema)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	body, err := avro.Marshal(s, v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return EncodeFrame(id, body)
}

// The whole point: bytes off a Confluent topic become a Hermod record.
func TestDecoderReadsAFramedRecord(t *testing.T) {
	srv, _ := registryServing(t, decUserSchema)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	d := NewDecoder(c)

	in := framedRecord(t, 1, decUserSchema, map[string]any{"id": int64(7), "name": "ada"})

	got, err := d.Decode(context.Background(), in)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["id"] != int64(7) || got["name"] != "ada" {
		t.Errorf("decoded = %#v, want id=7 name=ada", got)
	}
}

// Parsing a schema on every record would be as expensive as fetching one. The
// registry client caches the schema text; the decoder must also cache the
// *parsed* form, which is the costly half.
func TestDecoderCachesTheParsedSchema(t *testing.T) {
	srv, calls := registryServing(t, decUserSchema)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	d := NewDecoder(c)

	in := framedRecord(t, 1, decUserSchema, map[string]any{"id": int64(1), "name": "x"})
	for i := 0; i < 10; i++ {
		if _, err := d.Decode(context.Background(), in); err != nil {
			t.Fatalf("Decode #%d: %v", i, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("registry received %d requests for 10 records, want 1", n)
	}
	if n := d.ParsedCacheLen(); n != 1 {
		t.Errorf("parsed-schema cache holds %d entries, want 1", n)
	}
}

// The parsed-schema cache is keyed by a schema id that arrives on the wire, so
// it is the same attacker-chosen key the client's cache is, and needs the same
// bound.
func TestParsedSchemaCacheIsBounded(t *testing.T) {
	srv, _ := registryServing(t, decUserSchema)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	const limit = 8
	d := NewDecoder(c, WithParsedCacheSize(limit))

	for id := uint32(1); id <= 100; id++ {
		in := framedRecord(t, id, decUserSchema, map[string]any{"id": int64(1), "name": "x"})
		if _, err := d.Decode(context.Background(), in); err != nil {
			t.Fatalf("Decode(id=%d): %v", id, err)
		}
		if n := d.ParsedCacheLen(); n > limit {
			t.Fatalf("after %d ids the parsed cache holds %d entries, want at most %d", id, n, limit)
		}
	}
	if n := d.ParsedCacheLen(); n == 0 {
		t.Error("parsed cache is empty after 100 records — eviction is flushing it entirely")
	}
}

// Unframed input is the common misconfiguration: a plain-JSON topic pointed at
// a registry-aware source. It must say so.
func TestDecoderRejectsUnframedInput(t *testing.T) {
	srv, _ := registryServing(t, decUserSchema)
	c, _ := NewClient(Config{BaseURL: srv.URL})
	d := NewDecoder(c)

	_, err := d.Decode(context.Background(), []byte(`{"id":1}`))
	if err == nil {
		t.Fatal("Decode accepted plain JSON, want an error")
	}
	if !strings.Contains(err.Error(), "magic byte") {
		t.Errorf("error %q does not explain that the record is not framed", err)
	}
}

// A schema id the registry does not know is a poison message, not an outage,
// and the two want opposite handling.
func TestDecoderSurfacesAMissingSchemaDistinctly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error_code":40403,"message":"Schema not found"}`)
	}))
	defer srv.Close()

	c, _ := NewClient(Config{BaseURL: srv.URL})
	d := NewDecoder(c)

	_, err := d.Decode(context.Background(), EncodeFrame(42, []byte{0x02}))
	if err == nil {
		t.Fatal("Decode succeeded against a registry with no such schema")
	}
	if !strings.Contains(err.Error(), "schema not found") {
		t.Errorf("error %q does not identify a missing schema", err)
	}
}

// The decoder must carry the bounds, not merely have them available. This is
// the assembly check: a limit configured on the decoder has to reach
// avrodecode, or every bound in that package is decoration.
func TestDecoderAppliesItsLimits(t *testing.T) {
	const arraySchema = `{"type":"record","name":"R","fields":[
		{"name":"items","type":{"type":"array","items":"long"}}]}`

	srv, _ := registryServing(t, arraySchema)
	c, _ := NewClient(Config{BaseURL: srv.URL})

	items := make([]any, 500)
	for i := range items {
		items[i] = int64(i)
	}
	in := framedRecord(t, 1, arraySchema, map[string]any{"items": items})

	t.Run("within the limit", func(t *testing.T) {
		d := NewDecoder(c, WithMaxCollection(1000))
		if _, err := d.Decode(context.Background(), in); err != nil {
			t.Errorf("Decode: %v", err)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		d := NewDecoder(c, WithMaxCollection(100))
		if _, err := d.Decode(context.Background(), in); err == nil {
			t.Error("Decode accepted 500 elements against a MaxCollection of 100 — " +
				"the configured limit never reached the decoder")
		}
	})
}

func TestNewDecoderRejectsANilClient(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewDecoder(nil) did not panic; a nil client fails later and further away")
		}
	}()
	_ = NewDecoder(nil)
}
