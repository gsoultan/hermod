package schemaregistry

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

// The Confluent wire format is five bytes of header followed by the encoded
// body: a zero magic byte, then the schema's registry id as a big-endian
// uint32. Everything downstream of Hermod that reads an Avro topic — ksqlDB,
// Connect, the Confluent consumers — demands those five bytes, and anything
// producing onto one supplies them. Getting the byte order wrong is the classic
// defect here and it is invisible until a foreign consumer reads the topic,
// which is why the expected bytes below are written out literally rather than
// computed the same way the implementation computes them.
func TestEncodeFrameWritesTheConfluentHeader(t *testing.T) {
	tests := []struct {
		name     string
		schemaID uint32
		payload  []byte
		want     []byte
	}{
		{
			name:     "typical id",
			schemaID: 1,
			payload:  []byte{0xAA, 0xBB},
			want:     []byte{0x00, 0x00, 0x00, 0x00, 0x01, 0xAA, 0xBB},
		},
		{
			name:     "id spanning all four bytes proves big-endian",
			schemaID: 0x01020304,
			payload:  []byte{0x99},
			want:     []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x99},
		},
		{
			// An Avro record whose fields are all defaults encodes to zero
			// bytes. A bare header is therefore a legal message, not a
			// truncated one.
			name:     "empty payload is legal",
			schemaID: 7,
			payload:  nil,
			want:     []byte{0x00, 0x00, 0x00, 0x00, 0x07},
		},
		{
			name:     "max id",
			schemaID: math.MaxUint32,
			payload:  []byte{0x01},
			want:     []byte{0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0x01},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeFrame(tc.schemaID, tc.payload)
			if !bytes.Equal(got, tc.want) {
				t.Errorf("EncodeFrame(%d, %#v) = %#v, want %#v", tc.schemaID, tc.payload, got, tc.want)
			}
		})
	}
}

// DecodeFrame only slices bytes. It deliberately does not decode the body: the
// body's format is the caller's business, and for Avro it reaches a decoder
// this repository does not allow (see pkg/infra/schema/avro_no_decode_test.go).
func TestDecodeFrameSplitsHeaderFromPayload(t *testing.T) {
	id, payload, err := DecodeFrame([]byte{0x00, 0x00, 0x00, 0x00, 0x2A, 'h', 'i'})
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if id != 42 {
		t.Errorf("schema id = %d, want 42", id)
	}
	if string(payload) != "hi" {
		t.Errorf("payload = %q, want %q", payload, "hi")
	}
}

// Every one of these is a real operational mistake rather than a hypothetical:
// pointing a registry-aware reader at a plain-JSON topic is the common one, and
// it must say so rather than returning a plausible-looking schema id built out
// of whatever the first bytes happened to be.
func TestDecodeFrameRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		wantErr error
		// A substring the message must contain, so the operator is told what
		// actually went wrong rather than "invalid input".
		wantMsg string
	}{
		{
			name:    "empty",
			in:      nil,
			wantErr: ErrNotFramed,
			wantMsg: "empty",
		},
		{
			name:    "header truncated",
			in:      []byte{0x00, 0x00, 0x00},
			wantErr: ErrNotFramed,
			wantMsg: "5 bytes",
		},
		{
			name:    "plain JSON body, no header",
			in:      []byte(`{"id":1}`),
			wantErr: ErrNotFramed,
			wantMsg: "magic byte",
		},
		{
			name:    "non-zero magic byte",
			in:      []byte{0x01, 0x00, 0x00, 0x00, 0x01},
			wantErr: ErrNotFramed,
			wantMsg: "magic byte",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := DecodeFrame(tc.in)
			if err == nil {
				t.Fatalf("DecodeFrame(%#v) succeeded, want error", tc.in)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want errors.Is(..., %v)", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not mention %q — an operator cannot act on it", err, tc.wantMsg)
			}
		})
	}
}

// A frame must survive the round trip with its payload byte-identical. The
// aliasing check matters because EncodeFrame is called on the hot path and the
// obvious append-to-header implementation can hand back a slice that shares an
// array with a pooled buffer.
func TestFrameRoundTripDoesNotAliasThePayload(t *testing.T) {
	payload := []byte{0x01, 0x02, 0x03}
	frame := EncodeFrame(9, payload)

	id, got, err := DecodeFrame(frame)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	if id != 9 {
		t.Errorf("schema id = %d, want 9", id)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %#v, want %#v", got, payload)
	}

	// Mutating the caller's payload after framing must not change the frame.
	payload[0] = 0xFF
	if frame[5] != 0x01 {
		t.Errorf("EncodeFrame retained a reference to the caller's payload: frame[5] = %#x, want 0x01", frame[5])
	}
}
