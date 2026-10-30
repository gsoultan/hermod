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

	sinkredisset "github.com/gsoultan/hermod/pkg/comm/sink/redisset"
)

func TestRedisSetSinkIsBuiltFromItsConfig(t *testing.T) {
	snk, err := createSinkBase(SinkConfig{Type: "redis_set", Config: map[string]string{
		"addr": "127.0.0.1:1", "key": "user:{{.id}}",
	}})
	if err != nil {
		t.Fatalf("createSinkBase: %v", err)
	}
	if _, ok := snk.(*sinkredisset.RedisSetSink); !ok {
		t.Fatalf("got %T, want *redisset.RedisSetSink", snk)
	}
	_ = snk.Close()
}

// A sink that cannot write anything fails when the workflow starts, not on its
// first record.
func TestRedisSetSinkRefusesAConfigWithNoKey(t *testing.T) {
	if _, err := createSinkBase(SinkConfig{Type: "redis_set", Config: map[string]string{"addr": "127.0.0.1:1"}}); err == nil {
		t.Fatal("a redis_set sink with no key was built")
	}
}
