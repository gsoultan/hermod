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
	"strings"
	"testing"
)

// The exchange settings are checked when the sink is built, so a test of the
// connection or a workflow start reports them, not the first record.
func TestARabbitMQSinkWithBadExchangeSettingsIsRefused(t *testing.T) {
	_, err := CreateSink(SinkConfig{ID: "s", Type: "rabbitmq_queue", Config: map[string]string{
		"host": "mq", "exchange": "events", "exchange_type": "round-robin",
	}})
	if err == nil || !strings.Contains(err.Error(), "exchange type") {
		t.Fatalf("got %v, want the exchange type refused", err)
	}
}

func TestARabbitMQSinkBuildsForAQueueOrAnExchange(t *testing.T) {
	for name, cfg := range map[string]map[string]string{
		"queue only (saved before exchanges)": {"host": "mq", "queue_name": "orders"},
		"fanout exchange, no queue":           {"host": "mq", "exchange": "events", "exchange_type": "fanout"},
	} {
		t.Run(name, func(t *testing.T) {
			snk, err := CreateSink(SinkConfig{ID: "s", Type: "rabbitmq_queue", Config: cfg})
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			_ = snk.Close()
		})
	}
}
