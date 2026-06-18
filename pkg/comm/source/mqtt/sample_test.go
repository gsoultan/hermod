package mqtt

import (
	"testing"
)

// fakeMessage is a minimal paho.Message stub for exercising buildSampleMessage
// without a live broker.
type fakeMessage struct {
	topic     string
	messageID uint16
	qos       byte
	retained  bool
	payload   []byte
}

func (m *fakeMessage) Duplicate() bool   { return false }
func (m *fakeMessage) Qos() byte         { return m.qos }
func (m *fakeMessage) Retained() bool    { return m.retained }
func (m *fakeMessage) Topic() string     { return m.topic }
func (m *fakeMessage) MessageID() uint16 { return m.messageID }
func (m *fakeMessage) Payload() []byte   { return m.payload }
func (m *fakeMessage) Ack()              {}

func TestBuildSampleMessage(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantFields map[string]any
		wantAfter  string // when set, payload must be exposed via After() instead of Data()
		wantTopic  string
	}{
		{
			name:       "JSONObjectExposesFields",
			payload:    `{"user_id":42,"name":"alice","active":true}`,
			wantFields: map[string]any{"user_id": float64(42), "name": "alice", "active": true},
			wantTopic:  "sensors/temp",
		},
		{
			name:      "NonJSONFallsBackToAfter",
			payload:   "plain text payload",
			wantAfter: "plain text payload",
			wantTopic: "sensors/temp",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fm := &fakeMessage{
				topic:     tc.wantTopic,
				messageID: 7,
				qos:       1,
				payload:   []byte(tc.payload),
			}

			msg := buildSampleMessage(fm)

			if got := msg.Metadata()["topic"]; got != tc.wantTopic {
				t.Errorf("topic metadata = %q; want %q", got, tc.wantTopic)
			}
			if got := msg.Metadata()["sample"]; got != "true" {
				t.Errorf("sample metadata = %q; want \"true\"", got)
			}

			if tc.wantAfter != "" {
				if got := string(msg.After()); got != tc.wantAfter {
					t.Errorf("After() = %q; want %q", got, tc.wantAfter)
				}
				return
			}

			data := msg.Data()
			for k, want := range tc.wantFields {
				got, ok := data[k]
				if !ok {
					t.Errorf("missing field %q in sample data", k)
					continue
				}
				if got != want {
					t.Errorf("field %q = %v (%T); want %v (%T)", k, got, got, want, want)
				}
			}
		})
	}
}
