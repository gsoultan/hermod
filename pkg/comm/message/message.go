package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/gsoultan/hermod"
)

var (
	trailingCommaBraceRegex   = regexp.MustCompile(`,(\s*})`)
	trailingCommaBracketRegex = regexp.MustCompile(`,(\s*])`)
)

// TryFixJSON attempts to fix common JSON issues like trailing commas to make unmarshaling more lenient.
//
// It works on the bytes rather than on a string copy of them. The first thing
// this does is decide whether the payload even looks like JSON, and it used to
// copy the whole body into a string to find out — which for a text, CSV or
// binary body is the answer "no" at the cost of a full copy, on every message.
func TryFixJSON(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)

	// If it ends with a period, trim it (common in issue descriptions)
	trimmed = bytes.TrimSuffix(trimmed, []byte("."))
	trimmed = bytes.TrimSpace(trimmed)

	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return nil
	}

	// Remove trailing commas before closing braces/brackets using regex for robustness
	fixed := trailingCommaBraceRegex.ReplaceAll(trimmed, []byte("$1"))
	fixed = trailingCommaBracketRegex.ReplaceAll(fixed, []byte("$1"))

	return fixed
}

// canBeJSONObject reports whether payload could possibly unmarshal into a
// map[string]any, judged from its first meaningful byte.
//
// Only an object literal and null can. Everything else — an array, a scalar, a
// quoted string, a CSV line, a protobuf frame — cannot, and letting
// json.Unmarshal establish that costs a scan of the whole body plus a wrapped
// syntax error, twice, before TryFixJSON copies the body to reach the same
// conclusion a third time.
//
// null is the case that makes this a positive test rather than a check for
// '{': it decodes into a nil map *without* an error, so treating it as a
// non-object would change what a null payload decodes to.
func canBeJSONObject(payload []byte) bool {
	for _, c := range payload {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', 'n':
			return true
		default:
			return false
		}
	}
	return false
}

// SanitizeValue converts special types (like UUIDs) to JSON-friendly strings.
func SanitizeValue(v any) any {
	if v == nil {
		return nil
	}

	// Fast path for common types
	switch val := v.(type) {
	case string, int, int32, int64, float32, float64, bool, uint32, uint64:
		return v
	case uuid.UUID:
		return val.String()
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
		v = rv.Interface()
		// Re-check for common types after de-referencing
		switch val := v.(type) {
		case string, int, int32, int64, float32, float64, bool, uint32, uint64:
			return v
		case uuid.UUID:
			return val.String()
		}
	}

	// Handle byte slices and arrays that might be UUIDs
	if (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() == 16 && rv.Type().Elem().Kind() == reflect.Uint8 {
		var b [16]byte
		if rv.Kind() == reflect.Slice {
			copy(b[:], rv.Bytes())
		} else {
			for i := range 16 {
				b[i] = uint8(rv.Index(i).Uint())
			}
		}
		// We only convert if it looks like a valid UUID to avoid false positives
		u, err := uuid.FromBytes(b[:])
		if err == nil {
			return u.String()
		}
	}

	return v
}

// SanitizeMap sanitizes all values in a map.
func SanitizeMap(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = SanitizeValue(v)
	}
	return m
}

// DefaultMessage is a concrete implementation of the hermod.Message interface.
// It uses a sync.Pool to minimize allocations.
type DefaultMessage struct {
	mu        sync.RWMutex
	id        string
	operation hermod.Operation
	table     string
	schema    string
	before    []byte
	payload   []byte
	metadata  map[string]string
	data      map[string]any
	refCount  atomic.Int32
}

func (m *DefaultMessage) ID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.id
}

func (m *DefaultMessage) Operation() hermod.Operation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.operation
}

func (m *DefaultMessage) Table() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.table
}

func (m *DefaultMessage) Schema() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.schema
}

func (m *DefaultMessage) Before() []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return bytes.Clone(m.before)
}

func (m *DefaultMessage) After() []byte {
	return m.Payload()
}

func (m *DefaultMessage) Payload() []byte {
	m.mu.RLock()
	if len(m.payload) > 0 {
		defer m.mu.RUnlock()
		return bytes.Clone(m.payload)
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensurePayloadLocked()
	return bytes.Clone(m.payload)
}

// PayloadLen reports len(Payload()) without copying the payload.
//
// The engine asks for a payload's size far more often than for the payload:
// batch-by-bytes accounting sums it for every message and the per-write debug
// log reports it. Going through Payload() for that cloned the bytes and threw
// the copy away — together 2.8 GB of the 4.9 GB a 150k-message benchmark
// allocated, to produce two integers.
//
// It materialises the data map exactly as Payload() does, so the two never
// disagree about a message whose payload has not been marshalled yet;
// TestPayloadLenMatchesPayload holds them together.
func (m *DefaultMessage) PayloadLen() int {
	m.mu.RLock()
	if len(m.payload) > 0 {
		defer m.mu.RUnlock()
		return len(m.payload)
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensurePayloadLocked()
	return len(m.payload)
}

// ensurePayloadLocked marshals the data map into m.payload when no payload has
// been produced yet. Callers must hold the write lock.
func (m *DefaultMessage) ensurePayloadLocked() {
	if len(m.payload) > 0 || len(m.data) == 0 {
		return
	}
	if m.operation != "" {
		if a, ok := m.data["after"]; ok {
			m.payload, _ = json.Marshal(a)
			return
		}
	}
	m.payload, _ = json.Marshal(m.data)
}

func (m *DefaultMessage) Metadata() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.metadata)
}

// MetadataValue reads one metadata entry without copying the map.
//
// Metadata() has to clone — handing out the live map would race with a
// concurrent SetMetadata — so reading a single key through it copies every
// other entry to throw them away. The trace propagator does exactly that
// twice per sink write (traceparent, then tracestate), and it is not alone.
// This does the lookup under the same read lock, so it is as safe and costs
// nothing.
func (m *DefaultMessage) MetadataValue(key string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.metadata[key]
	return v, ok
}

func (m *DefaultMessage) MetadataRef() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metadata
}

func (m *DefaultMessage) Data() map[string]any {
	m.mu.RLock()
	if len(m.data) > 0 || len(m.payload) == 0 {
		defer m.mu.RUnlock()
		return maps.Clone(m.data)
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.data) == 0 && len(m.payload) > 0 {
		m.unmarshalPayloadLocked()
	}
	return maps.Clone(m.data)
}

// DataRef returns the live data map. The lock is released before it does, so a
// caller reading or writing the result holds nothing.
//
// That is safe today only because of who calls it, and the reasoning is not
// visible from any one call site — so it is written down here. Three rules, and
// every caller satisfies one:
//
//   - Mutate only a private copy. foreach walks and deletes through the DataRef
//     of a Clone, and the PII scan — which recursively ranges the map, the exact
//     operation the runtime makes fatal — is handed a Clone on its own goroutine.
//     Clone deep-copies the data map, which is what makes both sound.
//   - One consumer per object. The traversal clones when a node has more than one
//     target, so two processNode goroutines never share a message.
//   - Writes happen only where ownership is exclusive. A message is taken by
//     exactly one worker goroutine, and when it is later fanned out to N sinks —
//     the same object, one goroutine each — every holder is a reader. Sinks
//     mutate only messages they construct themselves, which
//     TestSinksDoNotMutateMessagesTheyAreGiven enforces rather than trusts.
//
// Break any of those and this races SetData, which the runtime reports as
// `fatal error: concurrent map read and map write` and no recover() can contain.
// Prefer Data() unless the clone is measurably too expensive; ToMap() if the
// value outlives the caller's stack. And do not let this comment age the way
// OrderingKey's ownership argument did — it was true when written and false a
// caller later.
func (m *DefaultMessage) DataRef() map[string]any {
	m.mu.RLock()
	if len(m.data) > 0 || len(m.payload) == 0 {
		defer m.mu.RUnlock()
		return m.data
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.data) == 0 && len(m.payload) > 0 {
		m.unmarshalPayloadLocked()
	}
	return m.data
}

// NonObjectPayloadKey is where a payload that is not a JSON object is exposed.
//
// Sources are free to emit a bare string, a scalar, an array, or bytes that are
// not JSON at all. Such a payload has no field names to merge into the message
// root, so it is preserved under this single key instead. That keeps it
// addressable by transformations and, crucially, keeps it in the serialised
// output: previously only objects survived and everything else was silently
// dropped.
//
// The name is deliberately "payload": array payloads have always been exposed
// this way, and it is the template variable the UI advertises for notification
// sinks ({{.payload}}). Widening the existing key to cover strings and scalars
// keeps every working template working, where a new name would not.
const NonObjectPayloadKey = "payload"

// decodePayloadFields returns the fields a payload contributes to a message's
// data map. A JSON object contributes its own fields; anything else is returned
// as a single NonObjectPayloadKey entry holding the decoded JSON value, or the
// literal text when the payload is not JSON at all.
//
// Both the lazy Data() path and MarshalJSON go through here so that a message
// serialises identically whether or not something read it first.
func decodePayloadFields(payload []byte) map[string]any {
	if len(payload) == 0 {
		return nil
	}

	if canBeJSONObject(payload) {
		var obj map[string]any
		if err := json.Unmarshal(payload, &obj); err == nil {
			return obj
		}
		// Tolerate the usual hand-written JSON slips (trailing commas) before
		// concluding this is not an object.
		if fixed := TryFixJSON(payload); fixed != nil {
			if err := json.Unmarshal(fixed, &obj); err == nil {
				return obj
			}
		}
	}

	// Valid JSON, just not an object: keep the decoded value so consumers see
	// [1,2,3] as an array and 42 as a number rather than as text. Arrays have
	// always landed here, so existing templates keep resolving.
	var value any
	if err := json.Unmarshal(payload, &value); err == nil {
		return map[string]any{NonObjectPayloadKey: value}
	}

	// Not JSON at all — a plain string, a CSV line, an opaque blob.
	return map[string]any{NonObjectPayloadKey: string(payload)}
}

// jsonRawOrWrapped prepares bytes destined for a CDC envelope field.
//
// json.RawMessage is emitted verbatim and rejects bytes that are not valid
// JSON, so a non-JSON before/after image failed the entire marshal — and with
// it the sink write. Such bytes are wrapped under NonObjectPayloadKey
// instead, the same way the non-CDC path preserves them.
func jsonRawOrWrapped(b []byte) any {
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	return map[string]any{NonObjectPayloadKey: string(b)}
}

// afterImageLocked returns the bytes of a CDC message's after-image.
//
// ToMap and MarshalJSON both need this and each had its own copy, which drifted:
// ToMap unwrapped an explicit data["after"] and MarshalJSON did not, so the same
// message serialised as {"after":{...}} through one and {"after":{"after":{...}}}
// through the other. The two are compared against each other by
// nonobject_payload_test.go precisely because they have drifted before; keep
// them sharing this.
//
// Callers must hold m.mu.
func (m *DefaultMessage) afterImageLocked() []byte {
	if len(m.payload) > 0 {
		return m.payload
	}
	if len(m.data) == 0 {
		return nil
	}
	if a, ok := m.data["after"]; ok {
		b, _ := json.Marshal(a)
		return b
	}
	b, _ := json.Marshal(m.data)
	return b
}

func (m *DefaultMessage) unmarshalPayloadLocked() {
	if m.data == nil {
		m.data = make(map[string]any)
	}
	maps.Copy(m.data, decodePayloadFields(m.payload))
}

func (m *DefaultMessage) Clone() hermod.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clone := AcquireMessage()
	if clone == m {
		// Use-after-release: the pool handed back the very message being
		// cloned, which means its refcount reached zero while this reference
		// was still live. Locking it would block forever against the
		// m.mu.RLock() held above — an unkillable hang rather than a visible
		// error. Fall back to an off-pool message so the clone still succeeds.
		// See TestCloneAfterPoolReuseDoesNotDeadlock.
		clone = &DefaultMessage{
			metadata: make(map[string]string),
			data:     make(map[string]any),
		}
		clone.refCount.Store(1)
	}

	// The clone must be locked: a message can still be concurrently Reset by
	// another goroutine that is releasing it, so writing its fields unguarded
	// is a data race.
	clone.mu.Lock()
	defer clone.mu.Unlock()

	clone.id = m.id
	clone.operation = m.operation
	clone.table = m.table
	clone.schema = m.schema
	clone.before = append(clone.before[:0], m.before...)
	clone.payload = append(clone.payload[:0], m.payload...)

	// Clear maps before copying
	clear(clone.metadata)
	clear(clone.data)

	// Metadata values are strings, so copying the map is enough.
	maps.Copy(clone.metadata, m.metadata)

	// Data values are not. A jsonb column decodes to a map[string]any, and the
	// traversal clones a message once per branch on every fan-out — so with a
	// top-level copy both branches held the *same* nested map. A transformation
	// writing into it on one branch was visible on the other, and the two
	// branches delivered each other's data with nothing logged.
	for k, v := range m.data {
		clone.data[k] = deepCopyValue(v)
	}
	return clone
}

// deepCopyValue returns a copy of v sharing no mutable state with it.
//
// Only the container kinds a decoded message can actually hold are copied;
// everything else is returned as-is, so a row of scalars — the common case —
// costs one type switch per field and allocates nothing.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = deepCopyValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = deepCopyValue(vv)
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(t))
		maps.Copy(out, t)
		return out
	case []byte:
		return append([]byte(nil), t...)
	case json.RawMessage:
		// Needed alongside []byte, not covered by it: RawMessage is a named type
		// and a type switch case on []byte does not match one. This is the shape
		// a CDC envelope arrives in, and SetBefore/SetPayload write back into
		// the message's own slice with append(b[:0], ...) — so without a copy
		// here the next write rewrites bytes a snapshot still points at.
		return json.RawMessage(append([]byte(nil), t...))
	default:
		return v
	}
}

// ToMap returns a snapshot of the message that shares no mutable state with it.
//
// Independence is the contract, not a detail. Every caller either marshals the
// result or hands it to another goroutine — a trace step recorded against
// PostgreSQL, a live-viewer frame fanned out to SSE subscribers — while the
// pipeline keeps transforming the message it came from.
//
// This used to copy the top level only, so each nested map[string]any in the
// snapshot was the same map the live message holds. SetData with a dotted key
// walks into that nested map and assigns in place, which put a write inside a
// map json.Marshal was already iterating on the trace goroutine. Concurrent map
// access is a runtime fatal error rather than a panic, so the recover() guarding
// that marshal could not contain it: the engine exited 2 mid-trace.
//
// The deep copy is paid by callers who are about to serialise this anyway, which
// costs strictly more than copying it. Data() and DataRef() are unchanged — they
// are the hot read path and they do not cross a goroutine boundary.
func (m *DefaultMessage) ToMap() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]any)

	// 1. If not a CDC event, merge data fields into root
	if m.operation == "" {
		for k, v := range m.data {
			res[k] = deepCopyValue(v)
		}

		// 2. If data is empty but payload is not, decode the payload into the
		// root. A payload that is not a JSON object lands under
		// NonObjectPayloadKey instead of being dropped: this used to ignore the
		// unmarshal error, silently discarding every string and scalar body.
		//
		// No copy needed: the decode allocates a fresh map every call and the
		// message does not keep it.
		if len(m.data) == 0 && len(m.payload) > 0 {
			maps.Copy(res, decodePayloadFields(m.payload))
		}
	}

	// 3. Add system fields
	if m.id != "" {
		res["id"] = m.id
	}
	if m.table != "" {
		res["table"] = m.table
	}
	if m.schema != "" {
		res["schema"] = m.schema
	}

	// CDC specific fields. Copied for the same reason the data fields are: the
	// envelope is a json.RawMessage over m.before or m.payload, and both of those
	// are written into in place. MarshalJSON shares jsonRawOrWrapped but needs
	// no copy — its value never leaves the call that holds the lock.
	if m.operation != "" {
		res["operation"] = m.operation
		if len(m.before) > 0 {
			res["before"] = deepCopyValue(jsonRawOrWrapped(m.before))
		}
		after := m.afterImageLocked()
		if len(after) > 0 {
			res["after"] = deepCopyValue(jsonRawOrWrapped(after))
		}
	}

	if len(m.metadata) > 0 {
		md := make(map[string]string, len(m.metadata))
		maps.Copy(md, m.metadata)
		res["metadata"] = md
	}

	return res
}

func (m *DefaultMessage) MarshalJSON() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]any)

	// 1. If not a CDC event, merge data fields into root
	// For CDC events, we keep the root clean and only include system fields + envelopes
	if m.operation == "" {
		maps.Copy(res, m.data)

		// 2. If data is empty but payload is not, decode the payload into the
		// root. A payload that is not a JSON object lands under
		// NonObjectPayloadKey instead of being dropped: this used to ignore the
		// unmarshal error, silently discarding every string and scalar body.
		if len(m.data) == 0 && len(m.payload) > 0 {
			maps.Copy(res, decodePayloadFields(m.payload))
		}
	}

	// 3. Add system fields. table/schema identify the record's origin and are
	// meaningful for both CDC and non-CDC messages, so they are emitted whenever
	// set (a non-CDC message produced by a source still carries its table).
	if m.id != "" {
		res["id"] = m.id
	}
	if m.table != "" {
		res["table"] = m.table
	}
	if m.schema != "" {
		res["schema"] = m.schema
	}

	// CDC specific fields - only if it's a CDC event (has an operation)
	if m.operation != "" {
		res["operation"] = m.operation
		if len(m.before) > 0 {
			res["before"] = jsonRawOrWrapped(m.before)
		}
		after := m.afterImageLocked()
		if len(after) > 0 {
			res["after"] = jsonRawOrWrapped(after)
		}
	}

	if len(m.metadata) > 0 {
		res["metadata"] = m.metadata
	}

	return json.Marshal(res)
}

// overReleases counts Release calls that arrive after a message's refcount has
// already reached zero. It is always a caller bug: the message is back in the
// pool and may already have been re-acquired and refilled, so the over-releasing
// owner is reading someone else's data. That shows up as messages delivered
// twice while others are never delivered at all, with the total conserved —
// silent data loss that no error path reports.
//
// It is counted rather than fatal because panicking in the message hot path
// would turn a recoverable accounting slip into an outage. Mirrors
// engine.PendingOverReleaseCount, which does the same for pendingMessage.
var overReleases atomic.Int64

// OverReleaseCount reports how many times a message was released after its
// refcount already reached zero. Any non-zero value means some owner is
// releasing a reference it does not hold; treat it as a correctness bug, not a
// tuning signal.
func OverReleaseCount() int64 { return overReleases.Load() }

// ResetOverReleaseCount zeroes the counter. Intended for tests that assert a
// pipeline runs with balanced reference counting.
func ResetOverReleaseCount() { overReleases.Store(0) }

func (m *DefaultMessage) Release() {
	n := m.refCount.Add(-1)
	if n == 0 {
		ReleaseMessage(m)
		return
	}
	if n < 0 {
		// Record it and nothing else. Clamping the count back to zero here was
		// tempting — a negative count never reaches zero again — but the write
		// races with AcquireMessage's StoreInt32(1): a message pooled by this
		// same over-release can already have been handed to a new owner, and
		// resetting the count under them makes the *next* legitimate Release
		// pool a message that is still in use. That converted an accounting slip
		// into real message loss, measured as an intermittent
		// TestEngineGracefulShutdown failure. Observe, do not mutate.
		overReleases.Add(1)
	}
}

func (m *DefaultMessage) Retain() {
	m.refCount.Add(1)
}

// RefCount reports the message's current reference count.
//
// It exists so the ownership contract can actually be asserted rather than
// reasoned about. Every node executor must return messages the caller owns one
// reference to; that invariant is invisible without being able to read the
// count, which is why a violation in the traversal's source branch went
// unnoticed until it was corrupting data. Use it in tests and diagnostics, not
// to make control-flow decisions: the value can change under you at any moment.
func (m *DefaultMessage) RefCount() int32 {
	return m.refCount.Load()
}

// Pooling limits.
//
// A pooled message keeps whatever it grew to: clear() empties a map but keeps
// its bucket array, and re-slicing a buffer to [:0] keeps its capacity. That is
// the point — reusing the allocation is what pooling is for — but it means one
// outlier message pins its footprint in the pool for the life of the process.
// A single 8 MB payload, or one 10,000-column row, and the pool never gives it
// back.
//
// So the common case keeps its allocation and anything grown out of proportion
// is handed back to the collector. The thresholds are set well above any
// ordinary row or payload, so a normal pipeline never trips them.
const (
	// maxPooledBufferBytes is the largest payload or before-image buffer a
	// pooled message will hold on to.
	maxPooledBufferBytes = 1 << 20 // 1 MiB
	// maxPooledMapEntries is the widest data or metadata map a pooled message
	// will hold on to.
	maxPooledMapEntries = 512
)

// Reset clears the message state so it can be reused.
func (m *DefaultMessage) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.id = ""
	m.operation = ""
	m.table = ""
	m.schema = ""
	m.clearPayloads()
	if len(m.metadata) > maxPooledMapEntries {
		m.metadata = make(map[string]string)
	} else {
		clear(m.metadata)
	}
}

// releaseOversizedBuffer returns b emptied, dropping the allocation when it has
// grown past what is worth keeping.
func releaseOversizedBuffer(b []byte) []byte {
	if cap(b) > maxPooledBufferBytes {
		return nil
	}
	return b[:0]
}

// ClearPayloads clears the data content of the message but keeps metadata/system fields.
func (m *DefaultMessage) ClearPayloads() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearPayloads()
}

func (m *DefaultMessage) clearPayloads() {
	m.before = releaseOversizedBuffer(m.before)
	m.payload = releaseOversizedBuffer(m.payload)
	if len(m.data) > maxPooledMapEntries {
		m.data = make(map[string]any)
	} else {
		clear(m.data)
	}
}

// ClearCachedPayload clears only the marshaled payload bytes.
func (m *DefaultMessage) ClearCachedPayload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.payload = m.payload[:0]
}

var messagePool = sync.Pool{
	New: func() any {
		return &DefaultMessage{
			metadata: make(map[string]string),
			data:     make(map[string]any),
		}
	},
}

// AcquireMessage gets a message from the pool.
func AcquireMessage() *DefaultMessage {
	m := messagePool.Get().(*DefaultMessage)
	m.refCount.Store(1)
	return m
}

// ReleaseMessage returns a message to the pool.
func ReleaseMessage(m hermod.Message) {
	if dm, ok := m.(*DefaultMessage); ok {
		dm.Reset()
		messagePool.Put(dm)
	}
}

// Setters for DefaultMessage
func (m *DefaultMessage) SetID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.id = id
}

func (m *DefaultMessage) SetOperation(op hermod.Operation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.operation = op
}

func (m *DefaultMessage) SetTable(table string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.table = table
}

func (m *DefaultMessage) SetSchema(schema string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schema = schema
}

func (m *DefaultMessage) SetBefore(before []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.before = append(m.before[:0], before...)
}

func (m *DefaultMessage) SetAfter(after []byte) {
	m.SetPayload(after)
}

func (m *DefaultMessage) SetPayload(payload []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.payload = append(m.payload[:0], payload...)
	// Clear data map to keep it in sync
	clear(m.data)
}

func (m *DefaultMessage) SetMetadata(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metadata[key] = value
}

func (m *DefaultMessage) SetData(key string, value any) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If data is empty but payload is not, materialise the payload first —
	// through the same helper the read side uses.
	//
	// This used to be a second, hand-written hydration that disagreed with
	// Data()/DataRef() in two ways, and the shape a message ended up with
	// depended on whether anything had read it before the first write.
	//
	// On a CDC message (operation set) it buried the row under a nested "after"
	// key and left the newly written field at the root, where Payload() — which
	// serialises only data["after"] — no longer included it. A transformation
	// that enriched such a message reported success and the enrichment never
	// reached the sink. db_lookup's cache-hit path is exactly this: it writes
	// without reading, so with the Cache TTL field empty every message after the
	// first silently lost its looked-up value.
	//
	// It also dropped any payload that is not a JSON object outright: the
	// unmarshal failed, nothing was stored, and the payload bytes are cleared at
	// the end of this function — so a plain-text or scalar body was destroyed by
	// the first SetData. decodePayloadFields keeps it under NonObjectPayloadKey.
	if len(m.data) == 0 && len(m.payload) > 0 {
		m.unmarshalPayloadLocked()
	}

	// "$" is the JSONPath document root, not a field. The read side
	// (evaluator.GetMsgValByPath) strips this prefix, and the UI teaches users
	// to write paths this way, so without the same treatment here a
	// targetField of "$.customer_name" silently buried the value under a
	// literal "$" key — present in the payload, absent everywhere anyone looked.
	key = strings.TrimPrefix(key, "$.")

	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := m.data
		for i := range len(parts) - 1 {
			next, ok := current[parts[i]].(map[string]any)
			if !ok {
				// Try to see if it's another type of map or if it needs to be created
				next = make(map[string]any)
				current[parts[i]] = next
			}
			current = next
		}
		current[parts[len(parts)-1]] = SanitizeValue(value)
	} else {
		m.data[key] = SanitizeValue(value)

		// Synchronize top-level system fields for consistency if they are not already set
		switch strings.ToLower(key) {
		case "id":
			if m.id == "" {
				if val, ok := value.(string); ok {
					m.id = val
				} else {
					m.id = fmt.Sprintf("%v", value)
				}
			}
		case "operation", "op":
			if m.operation == "" {
				if val, ok := value.(string); ok {
					m.operation = hermod.Operation(val)
				} else if val, ok := value.(hermod.Operation); ok {
					m.operation = val
				}
			}
		case "table":
			if m.table == "" {
				if val, ok := value.(string); ok {
					m.table = val
				}
			}
		case "schema":
			if m.schema == "" {
				if val, ok := value.(string); ok {
					m.schema = val
				}
			}
		}
	}
	// Clear payload bytes as they are now stale
	m.payload = m.payload[:0]
}
