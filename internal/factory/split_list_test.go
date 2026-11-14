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
	"reflect"
	"testing"
)

// The connection forms show a list the way people write one --
// "broker1:9092, broker2:9092" -- and the factory split it on the comma
// alone. Every entry after the first kept its leading space, so a Cassandra,
// ScyllaDB, Kafka or Elasticsearch connector configured exactly like the
// form's own example tried to resolve " node2" and failed on every host but
// the first ("gocql: dns error: lookup  127.0.0.1: no such host").
func TestSplitListTrimsEntriesAndDropsEmptyOnes(t *testing.T) {
	cases := map[string][]string{
		"node1:9042, node2:9042":          {"node1:9042", "node2:9042"},
		" broker1:9092 ,broker2:9092 ,  ": {"broker1:9092", "broker2:9092"},
		"a@example.com,,b@example.com":    {"a@example.com", "b@example.com"},
		"solo":                            {"solo"},
		"":                                nil,
		" , ":                             nil,
	}
	for in, want := range cases {
		if got := splitList(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitList(%q) = %q, want %q", in, got, want)
		}
	}
}
