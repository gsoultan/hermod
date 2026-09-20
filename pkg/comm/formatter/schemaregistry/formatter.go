// Package schemaregistry formats a Hermod message as a Confluent-framed
// record, so that a topic Hermod writes can be read by the consumers an
// existing Kafka estate already runs.
//
// It is a hermod.Formatter rather than a transformation on purpose. Framing is
// a serialisation concern, and a transformation that wrote framed bytes into
// the message payload would be undone by any sink configured with
// format: json, which re-serialises the whole message. Plugging in at the
// formatter seam means the bytes that leave the sink are the bytes this
// package produced.
//
// # Direction
//
// This package encodes only. Reading a Confluent-framed Avro topic needs an
// Avro *decoder*, and github.com/hamba/avro/v2 — the library Hermod already
// depends on — has three unfixed decoder denial-of-service advisories
// (GO-2026-5046/5047/5048, CVE-2026-46385). The module is archived upstream
// with no patched release. Hermod's acceptance of those advisories in
// scripts/govulncheck.sh rests on never decoding Avro, and that acceptance is
// enforced by a test. Decoding is therefore a separate decision about a
// dependency, not a missing function here.
package schemaregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/hamba/avro/v2"

	"github.com/gsoultan/hermod"
	sr "github.com/gsoultan/hermod/pkg/infra/schemaregistry"
)

// Type selects the body encoding. It mirrors the registry's own schema types.
type Type string

const (
	// Avro encodes the record as Avro binary, the default in most Kafka
	// estates and the only one where the framing carries real weight.
	Avro Type = "AVRO"
	// JSONSchema encodes the record as JSON, framed with the schema id so a
	// registry-aware consumer can validate it.
	JSONSchema Type = "JSON"
	// Protobuf is recognised so it can be refused with a reason. See New.
	Protobuf Type = "PROTOBUF"
)

// Config configures a Formatter.
type Config struct {
	// Client talks to the registry. Required. Share one across formatters so
	// they share its schema cache.
	Client *sr.Client

	// Subject is the registry subject to register under. Confluent's default
	// TopicNameStrategy makes this "<topic>-value".
	Subject string

	// Schema is the writer schema, as registry-ready text.
	Schema string

	// Type selects the body encoding. Defaults to Avro.
	Type Type
}

// Formatter encodes messages as Confluent-framed records.
//
// It is safe for concurrent use.
type Formatter struct {
	client  *sr.Client
	subject string
	schema  string
	typ     Type

	// parsed is the compiled Avro schema, nil for JSON.
	parsed avro.Schema

	// The registry id is resolved on first use rather than in New, so
	// constructing a Formatter does not require the registry to be reachable —
	// a pipeline that is built at start-up should not fail to build because a
	// dependency is briefly down.
	once  sync.Once
	id    uint32
	idErr error
}

// New validates cfg and returns a Formatter.
func New(cfg Config) (*Formatter, error) {
	if cfg.Client == nil {
		return nil, errors.New("schemaregistry formatter: Client is required")
	}
	if cfg.Subject == "" {
		return nil, errors.New("schemaregistry formatter: Subject is required")
	}
	if cfg.Schema == "" {
		return nil, errors.New("schemaregistry formatter: Schema is required")
	}

	typ := cfg.Type
	if typ == "" {
		typ = Avro
	}

	f := &Formatter{
		client:  cfg.Client,
		subject: cfg.Subject,
		schema:  cfg.Schema,
		typ:     typ,
	}

	switch typ {
	case Avro:
		parsed, err := avro.Parse(cfg.Schema)
		if err != nil {
			return nil, fmt.Errorf("schemaregistry formatter: parsing Avro schema: %w", err)
		}
		f.parsed = parsed
	case JSONSchema:
		// The schema is registered but not compiled: validation against it is
		// pkg/infra/schema's job, and doing it here would mean every message
		// paid for it twice.
		if !json.Valid([]byte(cfg.Schema)) {
			return nil, errors.New("schemaregistry formatter: Schema is not valid JSON")
		}
	case Protobuf:
		// Protobuf's registry framing puts a varint-encoded message-index array
		// between the schema id and the body, identifying which message in the
		// .proto file was used. Emitting an Avro-shaped frame instead would
		// produce bytes that every Protobuf consumer misreads, so this refuses
		// rather than appearing to work.
		return nil, errors.New(
			"schemaregistry formatter: Protobuf is not supported — its framing requires a " +
				"message-index array after the schema id, which this formatter does not write")
	default:
		return nil, fmt.Errorf("schemaregistry formatter: unknown schema type %q, want AVRO or JSON", typ)
	}

	return f, nil
}

// Format encodes msg and returns Confluent-framed bytes.
//
// hermod.Formatter carries no context, so the registry call is bounded by the
// client's own timeout rather than by the caller's deadline. That is the reason
// the schema id is resolved once and cached: a per-message uncancellable
// network call would be a much worse trade.
func (f *Formatter) Format(msg hermod.Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("schemaregistry formatter: message is nil")
	}

	id, err := f.schemaID()
	if err != nil {
		return nil, err
	}

	body, err := f.encode(msg)
	if err != nil {
		return nil, err
	}

	return sr.EncodeFrame(id, body), nil
}

// schemaID registers the schema once and returns its registry id.
//
// A failure is not memoised: once retries the function body only on the first
// call, so the error is stored and returned, but the sink's own retry creates a
// new pipeline rather than reusing a Formatter that has permanently failed.
func (f *Formatter) schemaID() (uint32, error) {
	f.once.Do(func() {
		f.id, f.idErr = f.client.RegisterSchema(context.Background(), f.subject, f.schema)
	})
	if f.idErr != nil {
		return 0, fmt.Errorf("schemaregistry formatter: registering %q: %w", f.subject, f.idErr)
	}
	return f.id, nil
}

func (f *Formatter) encode(msg hermod.Message) ([]byte, error) {
	data := msg.Data()

	switch f.typ {
	case Avro:
		body, err := avro.Marshal(f.parsed, data)
		if err != nil {
			return nil, fmt.Errorf(
				"schemaregistry formatter: Avro encode failed for message %s: %w", msg.ID(), err)
		}
		return body, nil
	case JSONSchema:
		body, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf(
				"schemaregistry formatter: JSON encode failed for message %s: %w", msg.ID(), err)
		}
		return body, nil
	default:
		// Unreachable: New rejects every other type.
		return nil, fmt.Errorf("schemaregistry formatter: unsupported type %q", f.typ)
	}
}
