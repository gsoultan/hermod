package message

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/user/hermod"
)

// SanitizeValue converts special types (like UUIDs) to JSON-friendly strings.
func SanitizeValue(v interface{}) interface{} {
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
	if rv.Kind() == reflect.Ptr {
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
			for i := 0; i < 16; i++ {
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
func SanitizeMap(m map[string]interface{}) map[string]interface{} {
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
	data      map[string]interface{}
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
	return m.before
}

func (m *DefaultMessage) After() []byte {
	return m.Payload()
}

func (m *DefaultMessage) Payload() []byte {
	m.mu.RLock()
	if len(m.payload) > 0 {
		defer m.mu.RUnlock()
		return m.payload
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check after acquiring write lock
	if len(m.payload) > 0 {
		return m.payload
	}

	// If payload is not set, try to marshal data
	if len(m.data) > 0 {
		m.payload, _ = json.Marshal(m.data)
		return m.payload
	}
	return m.payload
}

func (m *DefaultMessage) Metadata() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metadata
}

func (m *DefaultMessage) Data() map[string]interface{} {
	m.mu.RLock()
	if len(m.data) > 0 || len(m.payload) == 0 {
		defer m.mu.RUnlock()
		return m.data
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.data) == 0 && len(m.payload) > 0 {
		if err := json.Unmarshal(m.payload, &m.data); err != nil {
			// If not a map, try as a slice
			var slice []interface{}
			if err := json.Unmarshal(m.payload, &slice); err == nil {
				m.data["payload"] = slice
			}
		}
	}
	return m.data
}

func (m *DefaultMessage) Clone() hermod.Message {
	m.mu.RLock()
	defer m.mu.RUnlock()

	clone := AcquireMessage()
	clone.id = m.id
	clone.operation = m.operation
	clone.table = m.table
	clone.schema = m.schema
	clone.SetBefore(m.before)
	clone.SetPayload(m.payload)
	for k, v := range m.metadata {
		clone.metadata[k] = v
	}
	for k, v := range m.data {
		clone.data[k] = v
	}
	return clone
}

func (m *DefaultMessage) MarshalJSON() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	res := make(map[string]interface{})

	// 1. If not a CDC event, merge data fields into root
	// For CDC events, we keep the root clean and only include system fields + envelopes
	if m.operation == "" {
		for k, v := range m.data {
			res[k] = v
		}

		// 2. If data is empty but payload is not, unmarshal payload into root
		if len(m.data) == 0 && len(m.payload) > 0 {
			json.Unmarshal(m.payload, &res)
		}
	}

	// 3. Add system fields
	if m.id != "" {
		res["id"] = m.id
	}

	// CDC specific fields - only if it's a CDC event (has an operation)
	if m.operation != "" {
		res["operation"] = m.operation
		if m.table != "" {
			res["table"] = m.table
		}
		if m.schema != "" {
			res["schema"] = m.schema
		}
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

// Reset clears the message state so it can be reused.
func (m *DefaultMessage) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.id = ""
	m.operation = ""
	m.table = ""
	m.schema = ""
	m.clearPayloads()
	for k := range m.metadata {
		delete(m.metadata, k)
	}
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
	for k := range m.data {
		delete(m.data, k)
	}
}

// ClearCachedPayload clears only the marshaled payload bytes.
func (m *DefaultMessage) ClearCachedPayload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.payload = m.payload[:0]
}

var messagePool = sync.Pool{
	New: func() interface{} {
		return &DefaultMessage{
			metadata: make(map[string]string),
			data:     make(map[string]interface{}),
		}
	},
}

// AcquireMessage gets a message from the pool.
func AcquireMessage() *DefaultMessage {
	return messagePool.Get().(*DefaultMessage)
}

// ReleaseMessage returns a message to the pool.
func ReleaseMessage(m *DefaultMessage) {
	m.Reset()
	messagePool.Put(m)
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
	for k := range m.data {
		delete(m.data, k)
	}
}

func (m *DefaultMessage) SetMetadata(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.metadata[key] = value
}

func (m *DefaultMessage) SetData(key string, value interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// If data is empty but payload is not, try to unmarshal payload first
	if len(m.data) == 0 && len(m.payload) > 0 {
		var d map[string]interface{}
		if err := json.Unmarshal(m.payload, &d); err == nil {
			m.data = d
		}
	}

	if strings.Contains(key, ".") {
		parts := strings.Split(key, ".")
		current := m.data
		for i := 0; i < len(parts)-1; i++ {
			next, ok := current[parts[i]].(map[string]interface{})
			if !ok {
				// Try to see if it's another type of map or if it needs to be created
				next = make(map[string]interface{})
				current[parts[i]] = next
			}
			current = next
		}
		current[parts[len(parts)-1]] = SanitizeValue(value)
	} else {
		m.data[key] = SanitizeValue(value)
	}
	// Clear payload bytes as they are now stale
	m.payload = m.payload[:0]
}
