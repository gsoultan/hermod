// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
