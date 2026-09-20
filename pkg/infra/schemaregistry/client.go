package schemaregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// contentType is the media type Confluent's registry expects and returns.
const contentType = "application/vnd.schemaregistry.v1+json"

// Defaults chosen so a Config with only a BaseURL is safe to use.
const (
	defaultTimeout          = 10 * time.Second
	defaultCacheSize        = 1024
	defaultMaxResponseBytes = 1 << 20 // 1 MiB; a schema far larger than this is a bug or an attack.
	evictionBatch           = 64
)

// ErrSchemaNotFound reports a schema id the registry does not know. It is
// distinct from a transport failure because the two want opposite responses: a
// missing schema is a poison message and belongs in the dead-letter sink, where
// an unreachable registry is a retry.
var ErrSchemaNotFound = errors.New("schema not found in registry")

// Config configures a Client. Only BaseURL is required.
type Config struct {
	// BaseURL is the registry root, e.g. https://psrc-xxxxx.region.aws.confluent.cloud
	BaseURL string

	// Username and Password authenticate with HTTP basic auth. Confluent Cloud
	// issues these as an API key and secret. Leave empty for an unauthenticated
	// registry.
	Username string
	Password string

	// Timeout bounds a single registry request. Defaults to 10s.
	Timeout time.Duration

	// CacheSize bounds the number of cached schemas. Defaults to 1024.
	CacheSize int

	// MaxResponseBytes bounds a single response body. Defaults to 1 MiB.
	MaxResponseBytes int64

	// HTTPClient overrides the client used for requests. Supplying one means
	// Timeout is ignored; set it on the client instead.
	HTTPClient *http.Client
}

// Client talks to a Confluent-compatible Schema Registry.
//
// It is safe for concurrent use. Every engine worker sharing one instance is
// the intended deployment, because the cache is what keeps a registry lookup
// off the per-message path.
type Client struct {
	baseURL          string
	username         string
	password         string
	maxResponseBytes int64
	cacheSize        int
	http             *http.Client

	mu sync.RWMutex
	// byID caches schema text by registry id. Its key arrives on the wire, so
	// it is attacker-chosen and must stay bounded.
	byID map[uint32]string
	// bySchema caches registration results by subject+schema. Its key is
	// operator-supplied, but it shares the bound rather than relying on that.
	bySchema map[string]uint32
}

// NewClient validates cfg and returns a ready Client.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("schemaregistry: BaseURL is required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("schemaregistry: BaseURL is not a URL: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	size := cfg.CacheSize
	if size <= 0 {
		size = defaultCacheSize
	}
	maxBody := cfg.MaxResponseBytes
	if maxBody <= 0 {
		maxBody = defaultMaxResponseBytes
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	}

	return &Client{
		baseURL:          strings.TrimRight(cfg.BaseURL, "/"),
		username:         cfg.Username,
		password:         cfg.Password,
		maxResponseBytes: maxBody,
		cacheSize:        size,
		http:             hc,
		byID:             make(map[uint32]string),
		bySchema:         make(map[string]uint32),
	}, nil
}

// SchemaByID returns the schema text registered under id, from cache when
// possible.
func (c *Client) SchemaByID(ctx context.Context, id uint32) (string, error) {
	c.mu.RLock()
	cached, ok := c.byID[id]
	c.mu.RUnlock()
	if ok {
		return cached, nil
	}

	body, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/schemas/ids/%d", id), nil)
	if err != nil {
		return "", err
	}

	var resp struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("schemaregistry: decoding schema %d: %w", id, err)
	}

	c.mu.Lock()
	evictIfFull(c.byID, c.cacheSize)
	c.byID[id] = resp.Schema
	c.mu.Unlock()

	return resp.Schema, nil
}

// RegisterSchema registers schema under subject and returns its id, registering
// only once per distinct subject+schema.
//
// The registry is idempotent here: registering a schema that is already present
// returns the existing id rather than creating a second one, so a cache miss
// after an eviction costs a round trip, not a duplicate registration.
func (c *Client) RegisterSchema(ctx context.Context, subject, schema string) (uint32, error) {
	key := subject + "\x00" + schema

	c.mu.RLock()
	cached, ok := c.bySchema[key]
	c.mu.RUnlock()
	if ok {
		return cached, nil
	}

	payload, err := json.Marshal(struct {
		Schema string `json:"schema"`
	}{Schema: schema})
	if err != nil {
		return 0, fmt.Errorf("schemaregistry: encoding registration for %q: %w", subject, err)
	}

	body, err := c.do(ctx, http.MethodPost,
		"/subjects/"+url.PathEscape(subject)+"/versions", payload)
	if err != nil {
		return 0, err
	}

	var resp struct {
		ID uint32 `json:"id"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("schemaregistry: decoding registration for %q: %w", subject, err)
	}

	c.mu.Lock()
	evictIfFull(c.bySchema, c.cacheSize)
	c.bySchema[key] = resp.ID
	c.mu.Unlock()

	return resp.ID, nil
}

// CacheLen reports how many schemas are currently cached by id. It exists for
// the bound to be assertable.
func (c *Client) CacheLen() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}

// do issues one registry request and returns its body, bounded.
func (c *Client) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: building request for %s: %w", path, err)
	}
	req.Header.Set("Accept", contentType)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: requesting %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read one byte past the limit so an over-large body is detected rather
	// than silently truncated into a parse error that names the wrong cause.
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: reading response from %s: %w", path, err)
	}
	if int64(len(data)) > c.maxResponseBytes {
		return nil, fmt.Errorf(
			"schemaregistry: response from %s exceeds the %d byte limit", path, c.maxResponseBytes)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrSchemaNotFound, strings.TrimSpace(string(data)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("schemaregistry: %s returned %s: %s",
			path, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// evictIfFull drops a batch of arbitrary entries once the map is at capacity.
//
// The eviction is deliberately unordered rather than LRU, for the reason
// pkg/infra/evaluator gives for the same choice: an LRU lets whoever controls
// the key stream decide what stays resident. Schema ids arrive on the wire, so
// that is an attacker. Random eviction gives them nothing to steer.
func evictIfFull[K comparable, V any](m map[K]V, size int) {
	if len(m) < size {
		return
	}
	// Evict a slice of the cache, not the whole thing. A fixed batch larger
	// than the cache empties it on every overflow, which is bounded and
	// useless — every subsequent lookup becomes a registry round trip.
	batch := size / 8
	if batch > evictionBatch {
		batch = evictionBatch
	}
	if batch < 1 {
		batch = 1
	}

	n := 0
	for k := range m {
		delete(m, k)
		if n++; n >= batch {
			return
		}
	}
}
