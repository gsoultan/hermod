package rabbitmq

import (
	"testing"

	"github.com/google/uuid"
	"github.com/rabbitmq/rabbitmq-stream-go-client/pkg/amqp"
)

func TestMessageID(t *testing.T) {
	tests := []struct {
		name    string
		msg     *amqp.Message
		want    string
		wantGen bool // expect a generated UUID instead of a fixed value
	}{
		{
			name:    "nil message generates uuid",
			msg:     nil,
			wantGen: true,
		},
		{
			name:    "nil properties generates uuid",
			msg:     &amqp.Message{},
			wantGen: true,
		},
		{
			name: "string message-id is preserved",
			msg:  &amqp.Message{Properties: &amqp.MessageProperties{MessageID: "abc-123"}},
			want: "abc-123",
		},
		{
			name: "numeric message-id is stringified",
			msg:  &amqp.Message{Properties: &amqp.MessageProperties{MessageID: uint64(42)}},
			want: "42",
		},
		{
			name:    "empty string message-id falls back to uuid",
			msg:     &amqp.Message{Properties: &amqp.MessageProperties{MessageID: ""}},
			wantGen: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := messageID(tc.msg)
			if got == "" {
				t.Fatalf("messageID returned empty string")
			}
			if tc.wantGen {
				if _, err := uuid.Parse(got); err != nil {
					t.Errorf("expected a generated UUID, got %q (%v)", got, err)
				}
				return
			}
			if got != tc.want {
				t.Errorf("messageID = %q; want %q", got, tc.want)
			}
		})
	}
}
