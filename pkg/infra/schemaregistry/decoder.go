package schemaregistry

import (
	"context"
	"fmt"
	"sync"

	"github.com/hamba/avro/v2"

	"github.com/gsoultan/hermod/pkg/infra/avrodecode"
)

// Decoder turns a Confluent-framed record into a Hermod record.
//
// It composes the three pieces that job needs: the frame's schema id, the
// registry lookup that resolves it, and a bounded Avro decode.
//
// The decode is pkg/infra/avrodecode, not hamba/avro. hamba's decoder carries
// three unfixed denial-of-service advisories (GO-2026-5046/5047/5048,
// CVE-2026-46385) and its module is archived, so Hermod decodes with its own
// bounded implementation. hamba is still used to *parse* the schema, which is
// not the affected path.
//
// Decoder is safe for concurrent use.
type Decoder struct {
	client *Client
	limits avrodecode.Limits

	cacheSize int

	mu sync.RWMutex
	// parsed caches compiled schemas by registry id. Parsing is the expensive
	// half of a lookup, and the key arrives on the wire, so this is bounded for
	// the same reason the client's schema cache is.
	parsed map[uint32]avro.Schema
}

// DecoderOption configures a Decoder.
type DecoderOption func(*Decoder)

// WithParsedCacheSize bounds the compiled-schema cache. Defaults to 1024.
func WithParsedCacheSize(n int) DecoderOption {
	return func(d *Decoder) {
		if n > 0 {
			d.cacheSize = n
		}
	}
}

// WithMaxCollection bounds the elements in one decoded array or map.
func WithMaxCollection(n int) DecoderOption {
	return func(d *Decoder) { d.limits.MaxCollection = n }
}

// WithMaxBytes bounds one decoded string, bytes or fixed value.
func WithMaxBytes(n int) DecoderOption {
	return func(d *Decoder) { d.limits.MaxBytes = n }
}

// WithMaxDepth bounds decode nesting.
func WithMaxDepth(n int) DecoderOption {
	return func(d *Decoder) { d.limits.MaxDepth = n }
}

// NewDecoder returns a Decoder reading schemas through client.
//
// It panics on a nil client rather than deferring the failure to the first
// record, where it would surface as a confusing nil dereference inside a source.
func NewDecoder(client *Client, opts ...DecoderOption) *Decoder {
	if client == nil {
		panic("schemaregistry: NewDecoder requires a client")
	}
	d := &Decoder{
		client:    client,
		limits:    avrodecode.DefaultLimits(),
		cacheSize: defaultCacheSize,
		parsed:    make(map[uint32]avro.Schema),
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Decode reads one Confluent-framed Avro record.
func (d *Decoder) Decode(ctx context.Context, data []byte) (map[string]any, error) {
	id, body, err := DecodeFrame(data)
	if err != nil {
		return nil, err
	}

	schema, err := d.schema(ctx, id)
	if err != nil {
		return nil, err
	}

	out, err := avrodecode.Decode(schema, body, d.limits)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: decoding record written with schema %d: %w", id, err)
	}
	return out, nil
}

// schema resolves and compiles the schema for id, from cache when possible.
func (d *Decoder) schema(ctx context.Context, id uint32) (avro.Schema, error) {
	d.mu.RLock()
	cached, ok := d.parsed[id]
	d.mu.RUnlock()
	if ok {
		return cached, nil
	}

	text, err := d.client.SchemaByID(ctx, id)
	if err != nil {
		return nil, err
	}

	schema, err := avro.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("schemaregistry: parsing schema %d from the registry: %w", id, err)
	}

	d.mu.Lock()
	evictIfFull(d.parsed, d.cacheSize)
	d.parsed[id] = schema
	d.mu.Unlock()

	return schema, nil
}

// ParsedCacheLen reports how many compiled schemas are cached. It exists for
// the bound to be assertable.
func (d *Decoder) ParsedCacheLen() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.parsed)
}
