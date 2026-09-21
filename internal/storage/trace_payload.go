package storage

// How a trace step's payload is stored: one copy per distinct payload per
// message, compressed.
//
// message_trace_steps is the largest table Hermod owns — a row per node per
// message — and the payload column is most of it. Two things were being paid
// for that nothing reads back:
//
//  1. The same payload, over and over. Most nodes do not change the message.
//     workflow_start, the source ingest step, the validator and the router all
//     record it verbatim, and a transformation node records its output twice:
//     once under node.ID from the traversal, once under its transType from
//     doApplyTransformation. Measured across a realistic nine-step workflow,
//     66.6% of all payload bytes in the table were byte-identical to another
//     step of the *same message*.
//
//  2. No compression. JSON is the most compressible thing this database holds
//     and PostgreSQL does not compress it for us: a trace payload is well under
//     the ~2 KB TOAST threshold, so it sits in the heap verbatim. Measured on
//     PostgreSQL 18, 180k steps: toast 0.01 MB against a 127.84 MB heap.
//
// Measured, 20k traces of nine steps each with realistic incompressible CDC
// payloads:
//
//	                      PostgreSQL 18      SQLite
//	baseline                  135.20 MB   164.37 MB
//	compression only           95.23 MB   111.50 MB
//	dedup only                 65.93 MB    82.13 MB
//	both                       54.24 MB    71.14 MB   (2.49x / 2.31x)
//
// Dedup is worth more than compression here, which is the opposite of the usual
// intuition and the reason both are measured rather than assumed.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

const (
	// traceCodecRaw marks a stored value as plain JSON, traceCodecZstd as a
	// zstd frame.
	//
	// The codec travels in the value rather than being inferred from
	// configuration. A database written with compression on has to stay
	// readable after an operator turns it off — the alternative is that
	// flipping an environment variable silently blanks every trace written
	// before the restart.
	traceCodecRaw  byte = 0
	traceCodecZstd byte = 1

	// traceDecodeMaxBytes bounds decompression. These frames are ours, but a
	// corrupted row, a restored backup or a hand-edited value must not be able
	// to allocate the process to death. pkg/infra/avrodecode exists for the
	// same reason.
	traceDecodeMaxBytes = 64 << 20

	// TraceDedupCacheSize is how many (workflow, message, payload) facts are
	// remembered at once. A message's steps are recorded within milliseconds of
	// each other, so the useful lifetime of an entry is short and a few
	// thousand slots cover far more concurrency than the trace recorder's own
	// slot limit allows. The bound is not optional: the key contains a message
	// id, which is whatever the source supplied.
	TraceDedupCacheSize = 8192
)

var (
	traceCodecOnce sync.Once
	traceEncoder   *zstd.Encoder
	traceDecoder   *zstd.Decoder
)

func traceCodec() (*zstd.Encoder, *zstd.Decoder) {
	traceCodecOnce.Do(func() {
		// SpeedDefault rather than a higher level: this runs on the trace
		// recorder's bounded goroutine while a workflow is under load, and
		// tracing is switched on exactly when someone is trying to watch a busy
		// system. The extra ratio is not worth the latency.
		traceEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		traceDecoder, _ = zstd.NewReader(nil, zstd.WithDecoderMaxMemory(traceDecodeMaxBytes))
	})
	return traceEncoder, traceDecoder
}

// traceCompressionEnabled reports whether new payloads are compressed.
// HERMOD_TRACE_COMPRESSION=off stores readable JSON instead, for an operator
// who would rather keep `SELECT after_blob ...` legible in psql than have the
// space back.
func traceCompressionEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("HERMOD_TRACE_COMPRESSION")), "off")
}

// tracePayloadHash identifies a payload by its content, within one message.
//
// 128 bits of SHA-256, not 64. The bytes hashed are the message's own data,
// which in a CDC pipeline is whatever the upstream database contained and in a
// webhook pipeline is whatever the caller sent. A 64-bit content address over
// input someone else chooses is a 2^32 search away from showing one node's
// payload under another node's name — and a trace viewer that can be made to
// lie about which node produced what is worse than a slightly larger table.
func TracePayloadHash(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:16]
}

// encodeTracePayload turns marshalled JSON into the bytes stored in after_blob.
func EncodeTracePayload(raw []byte) []byte {
	if enc, _ := traceCodec(); enc != nil && traceCompressionEnabled() {
		// Compressing into a buffer that already holds the codec byte keeps
		// this to one allocation.
		out := enc.EncodeAll(raw, append(make([]byte, 0, len(raw)/2+8), traceCodecZstd))
		// A short payload can compress to more than it started as. Storing the
		// larger of the two to satisfy a flag would be silly.
		if len(out) < len(raw)+1 {
			return out
		}
	}
	return append(append(make([]byte, 0, len(raw)+1), traceCodecRaw), raw...)
}

// decodeTracePayload recovers the JSON behind a stored after_blob value.
func DecodeTracePayload(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	switch blob[0] {
	case traceCodecRaw:
		return blob[1:], nil
	case traceCodecZstd:
		_, dec := traceCodec()
		if dec == nil {
			return nil, nil
		}
		return dec.DecodeAll(blob[1:], nil)
	default:
		// Not a value this code wrote — a '{' is 0x7B, so bare JSON lands
		// here. Hand it back as it is rather than guessing at a codec.
		return blob, nil
	}
}

// traceDedupCache remembers which payloads already have their bytes in the
// table, so the steps that follow can store a reference instead of a copy.
//
// It is deliberately a cache and not an index, and it is consulted
// pessimistically. An entry is added only *after* an insert has actually
// succeeded, and "not in the cache" means "store the bytes". So a cold start, a
// second control-plane replica, an eviction under load or a race between two
// steps of the same message all cost a duplicate copy of a payload — never a
// reference with nothing behind it. Getting that direction the wrong way round
// would turn a full table into a blank trace viewer.
type TraceDedupCache struct {
	mu   sync.Mutex
	seen map[string]struct{}
	ring []string
	next int
}

func NewTraceDedupCache(capacity int) *TraceDedupCache {
	if capacity <= 0 {
		capacity = TraceDedupCacheSize
	}
	return &TraceDedupCache{
		seen: make(map[string]struct{}, capacity),
		ring: make([]string, capacity),
	}
}

// traceDedupKey scopes a payload to one message.
//
// Dedup never crosses messages, though identical payloads across messages are
// common. Two reasons, and either alone decides it: the retention sweep deletes
// by timestamp, so a payload shared between an old message and a new one would
// vanish from the new one when the old one expired; and GetMessageTrace reads
// exactly one message's rows, so a cross-message reference would have nothing
// to resolve against without a second query.
func traceDedupKey(workflowID, messageID string, hash []byte) string {
	var b strings.Builder
	b.Grow(len(workflowID) + len(messageID) + len(hash) + 2)
	b.WriteString(workflowID)
	b.WriteByte(0)
	b.WriteString(messageID)
	b.WriteByte(0)
	b.Write(hash)
	return b.String()
}

func (c *TraceDedupCache) AlreadyStored(workflowID, messageID string, hash []byte) bool {
	key := traceDedupKey(workflowID, messageID, hash)
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.seen[key]
	return ok
}

func (c *TraceDedupCache) MarkStored(workflowID, messageID string, hash []byte) {
	key := traceDedupKey(workflowID, messageID, hash)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[key]; ok {
		return
	}
	// FIFO. Entries are useful for the few milliseconds a message spends in
	// the pipeline, so recency ordering would buy nothing over insertion order.
	if old := c.ring[c.next]; old != "" {
		delete(c.seen, old)
	}
	c.ring[c.next] = key
	c.next = (c.next + 1) % len(c.ring)
	c.seen[key] = struct{}{}
}

func (c *TraceDedupCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.seen)
}

// TracePayloadKey is TracePayloadHash rendered as hex.
//
// The document stores keep their payloads in a map, and a map key has to be a
// string there: a BSON field name, or a JSON object key. Keying that map by
// content is what makes dedup inherent for those backends rather than something
// a cache has to get right — writing the same payload twice writes the same key
// twice, which is a no-op.
func TracePayloadKey(raw []byte) string {
	return hex.EncodeToString(TracePayloadHash(raw))
}
