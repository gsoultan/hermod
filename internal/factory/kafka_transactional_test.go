// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
