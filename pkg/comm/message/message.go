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
	hermod "github.com/gsoultan/Hermod"
)

var (
	trailingCommaBraceRegex   = regexp.MustCompile(`,(\s*})`)
	trailingCommaBracketRegex = regexp.MustCompile(`,(\s*])`)
)

// TryFixJSON attempts to fix common JSON issues like trailing commas to make unmarshaling more lenient.
func TryFixJSON(data []byte) []byte {
	str := string(data)
	trimmed := strings.TrimSpace(str)

	// If it ends with a period, trim it (common in issue descriptions)
	trimmed = strings.TrimSuffix(trimmed, ".")
	trimmed = strings.TrimSpace(trimmed)

	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return nil
	}

	// Remove trailing commas before closing braces/brackets using regex for robustness
	fixed := trailingCommaBraceRegex.ReplaceAllString(trimmed, "$1")
	fixed = trailingCommaBracketRegex.ReplaceAllString(fixed, "$1")

	return []byte(fixed)
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

	// Re-check after acquiring write lock
	if len(m.payload) > 0 {
		return bytes.Clone(m.payload)
	}

	// If payload is not set, try to marshal data
	if len(m.data) > 0 {
		if m.operation != "" {
			if a, ok := m.data["after"]; ok {
				m.payload, _ = json.Marshal(a)
				return bytes.Clone(m.payload)
			}
		}
		m.payload, _ = json.Marshal(m.data)
		return bytes.Clone(m.payload)
	}
	return bytes.Clone(m.payload)
}

func (m *DefaultMessage) Metadata() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.metadata)
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

func (m *DefaultMessage) unmarshalPayloadLocked() {
	if err := json.Unmarshal(m.payload, &m.data); err != nil {
		// Try lenient approach
		if fixed := TryFixJSON(m.payload); fixed != nil {
			if err := json.Unmarshal(fixed, &m.data); err == nil {
				return
			}
		}
		// If still not a map, try as a slice
		var slice []any
		if err := json.Unmarshal(m.payload, &slice); err == nil {
			m.data["payload"] = slice
		}
	}
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

	maps.Copy(clone.metadata, m.metadata)
	maps.Copy(clone.data, m.data)
	return clone
}

func (m *DefaultMessage) ToMap() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]any)

	// 1. If not a CDC event, merge data fields into root
	if m.operation == "" {
		maps.Copy(res, m.data)

		// 2. If data is empty but payload is not, unmarshal payload into root
		if len(m.data) == 0 && len(m.payload) > 0 {
			json.Unmarshal(m.payload, &res)
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

	// CDC specific fields
	if m.operation != "" {
		res["operation"] = m.operation
		if len(m.before) > 0 {
			res["before"] = json.RawMessage(m.before)
		}
		after := m.payload
		if len(after) == 0 && len(m.data) > 0 {
			if a, ok := m.data["after"]; ok {
				after, _ = json.Marshal(a)
			} else {
				after, _ = json.Marshal(m.data)
			}
		}
		if len(after) > 0 {
			res["after"] = json.RawMessage(after)
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

		// 2. If data is empty but payload is not, unmarshal payload into root
		if len(m.data) == 0 && len(m.payload) > 0 {
			json.Unmarshal(m.payload, &res)
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
			res["before"] = json.RawMessage(m.before)
		}
		after := m.payload
		if len(after) == 0 && len(m.data) > 0 {
			after, _ = json.Marshal(m.data)
		}
		if len(after) > 0 {
			res["after"] = json.RawMessage(after)
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

// Reset clears the message state so it can be reused.
func (m *DefaultMessage) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.id = ""
	m.operation = ""
	m.table = ""
	m.schema = ""
	m.clearPayloads()
	clear(m.metadata)
}

// ClearPayloads clears the data content of the message but keeps metadata/system fields.
func (m *DefaultMessage) ClearPayloads() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearPayloads()
}

func (m *DefaultMessage) clearPayloads() {
	m.before = m.before[:0]
	m.payload = m.payload[:0]
	clear(m.data)
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

	// If data is empty but payload is not, try to unmarshal payload first
	if len(m.data) == 0 && len(m.payload) > 0 {
		var d map[string]any
		payloadToUnmarshal := m.payload
		if err := json.Unmarshal(payloadToUnmarshal, &d); err == nil {
			if m.operation != "" {
				m.data["after"] = d
			} else {
				m.data = d
			}
		} else {
			// Try lenient approach
			if fixed := TryFixJSON(payloadToUnmarshal); fixed != nil {
				if err := json.Unmarshal(fixed, &d); err == nil {
					if m.operation != "" {
						m.data["after"] = d
					} else {
						m.data = d
					}
				}
			}
		}
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
