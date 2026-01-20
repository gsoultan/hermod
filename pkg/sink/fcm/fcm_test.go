package fcm

import (
	"context"
	"testing"

	"firebase.google.com/go/v4/messaging"
	"github.com/user/hermod"
	"github.com/user/hermod/pkg/formatter/json"
)

type mockMessage struct {
	hermod.Message
	id        string
	operation hermod.Operation
	table     string
	schema    string
	metadata  map[string]string
}

func (m *mockMessage) ID() string                   { return m.id }
func (m *mockMessage) Operation() hermod.Operation  { return m.operation }
func (m *mockMessage) Table() string                { return m.table }
func (m *mockMessage) Schema() string               { return m.schema }
func (m *mockMessage) Before() []byte               { return nil }
func (m *mockMessage) After() []byte                { return []byte(`{"foo":"bar"}`) }
func (m *mockMessage) Metadata() map[string]string  { return m.metadata }
func (m *mockMessage) Data() map[string]interface{} { return nil }
func (m *mockMessage) Clone() hermod.Message        { return m }
func (m *mockMessage) ClearPayloads()               {}

func TestFCMSink_Write_Error(t *testing.T) {
	// We can't easily test a successful Send without a real service account
	// or a complex mock. But we can test the validation logic.

	formatter := json.NewJSONFormatter()
	sink := &FCMSink{
		client:    &messaging.Client{}, // Mock client to bypass ensureConnected or just set it
		formatter: formatter,
	}

	tests := []struct {
		name    string
		msg     hermod.Message
		wantErr string
	}{
		{
			name: "no destination",
			msg: &mockMessage{
				id:        "123",
				operation: hermod.OpCreate,
				table:     "users",
				metadata:  map[string]string{},
			},
			wantErr: "fcm destination (token, topic, or condition) not found in message metadata",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sink.Write(context.Background(), tt.msg)
			if err == nil {
				t.Error("expected error, got nil")
				return
			}
			if err.Error() != tt.wantErr {
				t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestNewFCMSink(t *testing.T) {
	formatter := json.NewJSONFormatter()
	sink, err := NewFCMSink(`{"type": "service_account"}`, formatter)
	if err != nil {
		t.Errorf("expected no error from lazy NewFCMSink, got %v", err)
	}
	if sink.credentialsJSON != `{"type": "service_account"}` {
		t.Errorf("credentialsJSON not set correctly")
	}
}
