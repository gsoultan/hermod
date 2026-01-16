package message

import (
	"encoding/json"
	"reflect"
	"sync"

	"github.com/google/uuid"
	"github.com/user/hermod"
)

// SanitizeValue converts special types (like UUIDs) to JSON-friendly strings.
func SanitizeValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
		v = rv.Interface()
	}

	// Handle standard UUID types
	if u, ok := v.(uuid.UUID); ok {
		return u.String()
	}

	// Handle byte slices and arrays that might be UUIDs
	if (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() == 16 && rv.Type().Elem().Kind() == reflect.Uint8 {
		var b [16]byte
		for i := 0; i < 16; i++ {
			b[i] = uint8(rv.Index(i).Uint())
		}
		// We only convert if it looks like a valid UUID to avoid false positives
		// but for CDC it's usually safe to assume 16-byte binary is a UUID or something that should be hex/string
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
	return m.id
}

func (m *DefaultMessage) Operation() hermod.Operation {
	return m.operation
}

func (m *DefaultMessage) Table() string {
	return m.table
}

func (m *DefaultMessage) Schema() string {
	return m.schema
}

func (m *DefaultMessage) Before() []byte {
	return m.before
}

func (m *DefaultMessage) After() []byte {
	return m.payload
}

func (m *DefaultMessage) Payload() []byte {
	if len(m.payload) > 0 {
		return m.payload
	}
	if len(m.data) > 0 {
		// Cache this? For now just marshal.
		// In production, we might want to avoid marshaling here if possible.
		b, _ := json.Marshal(m.data)
		return b
	}
	return m.payload
}

func (m *DefaultMessage) Metadata() map[string]string {
	return m.metadata
}

func (m *DefaultMessage) Data() map[string]interface{} {
	return m.data
}

func (m *DefaultMessage) Clone() hermod.Message {
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
	res := make(map[string]interface{})

	// 1. Merge data fields into root
	for k, v := range m.data {
		res[k] = v
	}

	// 2. If data is empty but payload is not, unmarshal payload into root
	if len(m.data) == 0 && len(m.payload) > 0 {
		json.Unmarshal(m.payload, &res)
	}

	// 3. Add system fields
	if m.id != "" {
		res["id"] = m.id
	}
	if m.operation != "" {
		res["operation"] = m.operation
	}
	if m.table != "" {
		res["table"] = m.table
	}
	if m.schema != "" {
		res["schema"] = m.schema
	}
	if len(m.before) > 0 {
		res["before"] = json.RawMessage(m.before)
	}
	if len(m.metadata) > 0 {
		res["metadata"] = m.metadata
	}

	return json.Marshal(res)
}

// Reset clears the message state so it can be reused.
func (m *DefaultMessage) Reset() {
	m.id = ""
	m.operation = ""
	m.table = ""
	m.schema = ""
	m.ClearPayloads()
	for k := range m.metadata {
		delete(m.metadata, k)
	}
}

// ClearPayloads clears the data content of the message but keeps metadata/system fields.
func (m *DefaultMessage) ClearPayloads() {
	m.before = m.before[:0]
	m.payload = m.payload[:0]
	for k := range m.data {
		delete(m.data, k)
	}
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
	m.id = id
}

func (m *DefaultMessage) SetOperation(op hermod.Operation) {
	m.operation = op
}

func (m *DefaultMessage) SetTable(table string) {
	m.table = table
}

func (m *DefaultMessage) SetSchema(schema string) {
	m.schema = schema
}

func (m *DefaultMessage) SetBefore(before []byte) {
	m.before = append(m.before[:0], before...)
}

func (m *DefaultMessage) SetAfter(after []byte) {
	m.SetPayload(after)
}

func (m *DefaultMessage) SetPayload(payload []byte) {
	m.payload = append(m.payload[:0], payload...)
	// Clear data map to keep it in sync
	for k := range m.data {
		delete(m.data, k)
	}
}

func (m *DefaultMessage) SetMetadata(key, value string) {
	m.metadata[key] = value
}

func (m *DefaultMessage) SetData(key string, value interface{}) {
	m.data[key] = SanitizeValue(value)
	// Clear payload bytes as they are now stale
	m.payload = m.payload[:0]
}
