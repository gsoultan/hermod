// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package factory

import (
	"testing"

	sinkkafka "github.com/gsoultan/hermod/pkg/comm/sink/kafka"
)

// transactional_id used to be refused, because the old client could not open a
// transaction. It now turns the sink transactional, and must reach the sink:
// a setting the factory drops would leave an operator believing they have
// exactly-once delivery while the sink writes at-least-once.
func TestKafkaSinkTransactionalIDReachesTheSink(t *testing.T) {
	cases := map[string]string{"": "", "  ": "", "hermod-orders": "hermod-orders", " padded ": "padded"}
	for in, want := range cases {
		snk, err := createSinkBase(SinkConfig{Type: "kafka", Config: map[string]string{
			"brokers": "127.0.0.1:1", "topic": "t", "transactional_id": in,
		}})
		if err != nil {
			t.Fatalf("transactional_id %q: %v", in, err)
		}
		ks, ok := snk.(*sinkkafka.KafkaSink)
		if !ok {
			t.Fatalf("got %T, want *kafka.KafkaSink", snk)
		}
		if got := ks.TransactionalID(); got != want {
			t.Errorf("transactional_id %q reached the sink as %q, want %q", in, got, want)
		}
		_ = ks.Close()
	}
}
