// Package schemaregistry speaks the Confluent Schema Registry wire format and
// REST API.
//
// Hermod already had a schema registry of its own (pkg/infra/schema) that
// validates a message against a stored Avro, JSON Schema or Protobuf schema.
// That is a different thing from this package. This one is about interop: a
// Kafka estate that uses Confluent Schema Registry frames every record with a
// five-byte header naming the schema it was written with, and a consumer that
// does not understand the header reads the record as corrupt. Without this,
// Hermod can neither consume an existing Avro topic nor write one that an
// existing consumer can read.
//
// # What this package does not do
//
// It does not decode Avro bodies. DecodeFrame splits the header off and hands
// back the remaining bytes; interpreting them is the caller's job. That is a
// deliberate boundary, not an omission — see the package comment on
// ErrDecodeUnsupported and pkg/infra/schema/avro_no_decode_test.go.
package schemaregistry

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// magicByte prefixes every registry-framed record. Confluent reserves zero for
// the current framing; a different value means a future format this code does
// not understand, and is reported rather than skipped over.
const magicByte = 0x00

// headerLen is the magic byte plus a big-endian uint32 schema id.
const headerLen = 5

// ErrNotFramed reports input that is not a Confluent-framed record. The
// overwhelmingly common cause is a reader pointed at a topic that was never
// written through a registry — plain JSON, or a raw string.
var ErrNotFramed = errors.New("not a Confluent-framed record")

// EncodeFrame prepends the Confluent header to an already-encoded body.
//
// The returned slice never aliases payload: it is sized exactly and copied, so
// a caller reusing its encode buffer cannot retroactively change a frame that
// has already been handed to a sink.
func EncodeFrame(schemaID uint32, payload []byte) []byte {
	frame := make([]byte, headerLen+len(payload))
	frame[0] = magicByte
	binary.BigEndian.PutUint32(frame[1:headerLen], schemaID)
	copy(frame[headerLen:], payload)
	return frame
}

// DecodeFrame splits a Confluent-framed record into its schema id and body.
//
// The returned payload aliases data; callers that retain it beyond the lifetime
// of the input buffer must copy it. This is the hot path for every consumed
// record, and the copy is the caller's to decide on.
func DecodeFrame(data []byte) (schemaID uint32, payload []byte, err error) {
	if len(data) == 0 {
		return 0, nil, fmt.Errorf("%w: message is empty", ErrNotFramed)
	}
	if data[0] != magicByte {
		return 0, nil, fmt.Errorf(
			"%w: first byte is %#02x, want magic byte %#02x — the topic is probably not written through a schema registry",
			ErrNotFramed, data[0], magicByte)
	}
	if len(data) < headerLen {
		return 0, nil, fmt.Errorf(
			"%w: header needs %d bytes, message has %d", ErrNotFramed, headerLen, len(data))
	}
	return binary.BigEndian.Uint32(data[1:headerLen]), data[headerLen:], nil
}
