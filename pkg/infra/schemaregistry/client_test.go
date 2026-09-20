package schemaregistry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testSchema = `{"type":"record","name":"User","fields":[{"name":"id","type":"long"}]}`

// newTestRegistry stands up a stub speaking the subset of the Confluent REST
// API this client uses, and counts requests so the caching tests can assert on
// them.
func newTestRegistry(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("/schemas/ids/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		id := strings.TrimPrefix(r.URL.Path, "/schemas/ids/")
		if id != "1" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"error_code":40403,"message":"Schema not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = fmt.Fprintf(w, `{"schema":%q}`, testSchema)
	})
	mux.HandleFunc("/subjects/", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		_, _ = fmt.Fprint(w, `{"id":1}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestClientFetchesASchemaByID(t *testing.T) {
	srv, _ := newTestRegistry(t)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := c.SchemaByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("SchemaByID: %v", err)
	}
	if got != testSchema {
		t.Errorf("schema = %q, want %q", got, testSchema)
	}
}

// A registry lookup per message would put a network round trip in front of
// every record. The cache is not an optimisation here, it is what makes the
// feature usable at all.
func TestClientCachesSchemaLookups(t *testing.T) {
	srv, calls := newTestRegistry(t)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := c.SchemaByID(context.Background(), 1); err != nil {
			t.Fatalf("SchemaByID #%d: %v", i, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("registry received %d requests for the same schema id, want 1", n)
	}
}

// The schema id comes off the wire, which means a hostile producer chooses it.
// An unbounded map keyed on it is a memory exhaustion primitive: 4 billion
// distinct ids, each one a fetch and a permanent entry. The cache must evict.
//
// Every id here resolves. An earlier version of this test reused the stub that
// answers only id 1, so ninety-nine of its hundred lookups were 404s that never
// cached anything — it passed with the bound deleted outright. A cache test
// whose ids do not cache is not testing the cache.
func TestSchemaCacheIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = fmt.Fprintf(w, `{"schema":%q}`, testSchema)
	}))
	defer srv.Close()

	const limit = 8
	c, err := NewClient(Config{BaseURL: srv.URL, CacheSize: limit})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	for id := uint32(1); id <= 100; id++ {
		if _, err := c.SchemaByID(context.Background(), id); err != nil {
			t.Fatalf("SchemaByID(%d): %v", id, err)
		}
		if n := c.CacheLen(); n > limit {
			t.Fatalf("after %d distinct ids the cache holds %d entries, want at most %d", id, n, limit)
		}
	}

	// A bound that evicts everything on each overflow is technically bounded
	// and useless: the next lookup of any id is a registry round trip. Assert
	// the cache is still doing its job.
	if n := c.CacheLen(); n == 0 {
		t.Error("cache is empty after 100 lookups — eviction is flushing it entirely, so nothing is ever a hit")
	}
}

func TestClientRegistersASchemaAndReturnsItsID(t *testing.T) {
	srv, _ := newTestRegistry(t)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	id, err := c.RegisterSchema(context.Background(), "users-value", testSchema)
	if err != nil {
		t.Fatalf("RegisterSchema: %v", err)
	}
	if id != 1 {
		t.Errorf("schema id = %d, want 1", id)
	}
}

// Registering on every message would be as bad as fetching on every message,
// and the subject+schema pair is operator-supplied rather than attacker-chosen,
// so it caches on the same bounded structure.
func TestClientCachesRegistrations(t *testing.T) {
	srv, calls := newTestRegistry(t)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	for i := 0; i < 5; i++ {
		if _, err := c.RegisterSchema(context.Background(), "users-value", testSchema); err != nil {
			t.Fatalf("RegisterSchema #%d: %v", i, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("registry received %d registrations for the same subject+schema, want 1", n)
	}
}

func TestClientReportsAMissingSchemaDistinctly(t *testing.T) {
	srv, _ := newTestRegistry(t)
	c, err := NewClient(Config{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.SchemaByID(context.Background(), 999)
	if !errors.Is(err, ErrSchemaNotFound) {
		t.Errorf("error = %v, want errors.Is(..., ErrSchemaNotFound)", err)
	}
}

// Confluent Cloud authenticates with an API key and secret over basic auth.
// Sending the request without them yields a 401 that reads like a registry
// outage, so this asserts the header is actually attached.
func TestClientSendsBasicAuthWhenConfigured(t *testing.T) {
	var gotUser, gotPass string
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, hadAuth = r.BasicAuth()
		_, _ = fmt.Fprintf(w, `{"schema":%q}`, testSchema)
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL, Username: "key", Password: "secret"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if _, err := c.SchemaByID(context.Background(), 1); err != nil {
		t.Fatalf("SchemaByID: %v", err)
	}

	if !hadAuth {
		t.Fatal("request carried no Authorization header")
	}
	if gotUser != "key" || gotPass != "secret" {
		t.Errorf("basic auth = %q/%q, want key/secret", gotUser, gotPass)
	}
}

// A registry that accepts the connection and then never answers must not pin
// the pipeline. Network Architect's rule: no unbounded waits.
func TestClientTimesOutAStalledRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	start := time.Now()
	if _, err := c.SchemaByID(context.Background(), 1); err == nil {
		t.Fatal("SchemaByID against a stalled registry succeeded, want a timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v to give up, want the configured 50ms to apply", elapsed)
	}
}

// The registry is a remote Hermod does not control. A compromised or broken one
// answering with an endless body must not be read into memory without limit.
func TestClientBoundsTheResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
		// Never stops.
		chunk := strings.Repeat("a", 4096)
		for {
			if _, err := fmt.Fprint(w, chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	c, err := NewClient(Config{BaseURL: srv.URL, MaxResponseBytes: 8192, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := c.SchemaByID(context.Background(), 1)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SchemaByID read an unbounded body to completion, want a size error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SchemaByID never returned — the response body is being read without a limit")
	}
}

func TestNewClientRejectsAnEmptyBaseURL(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("NewClient with no BaseURL succeeded, want an error")
	}
}
