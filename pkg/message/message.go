package message

import (
	"encoding/json"
	"sync"

	"github.com/user/hermod"
)

// DefaultMessage is a concrete implementation of the hermod.Message interface.
// It uses a sync.Pool to minimize allocations.
type DefaultMessage struct {
	id        string
	operation hermod.Operation
	table     string
	schema    string
	before    []byte
	after     []byte
	payload   []byte
	metadata  map[string]string
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
	return m.after
}

func (m *DefaultMessage) Payload() []byte {
	return m.payload
}

func (m *DefaultMessage) Metadata() map[string]string {
	return m.metadata
}

func (m *DefaultMessage) MarshalJSON() ([]byte, error) {
	type Alias struct {
		ID        string            `json:"id"`
		Operation hermod.Operation  `json:"operation"`
		Table     string            `json:"table"`
		Schema    string            `json:"schema"`
		Before    json.RawMessage   `json:"before,omitempty"`
		After     json.RawMessage   `json:"after,omitempty"`
		Payload   json.RawMessage   `json:"payload,omitempty"`
		Metadata  map[string]string `json:"metadata,omitempty"`
	}
	return json.Marshal(Alias{
		ID:        m.id,
		Operation: m.operation,
		Table:     m.table,
		Schema:    m.schema,
		Before:    json.RawMessage(m.before),
		After:     json.RawMessage(m.after),
		Payload:   json.RawMessage(m.payload),
		Metadata:  m.metadata,
	})
}

// Reset clears the message state so it can be reused.
func (m *DefaultMessage) Reset() {
	m.id = ""
	m.operation = ""
	m.table = ""
	m.schema = ""
	m.before = m.before[:0]
	m.after = m.after[:0]
	m.payload = m.payload[:0]
	for k := range m.metadata {
		delete(m.metadata, k)
	}
}

var messagePool = sync.Pool{
	New: func() interface{} {
		return &DefaultMessage{
			metadata: make(map[string]string),
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
	m.after = append(m.after[:0], after...)
}

func (m *DefaultMessage) SetPayload(payload []byte) {
	m.payload = append(m.payload[:0], payload...)
}

func (m *DefaultMessage) SetMetadata(key, value string) {
	m.metadata[key] = value
}
