// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package redisset

import (
	"errors"
	"net"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/rediskv"
)

func TestRedisSetExplainsAConnectionRefused(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	snk, err := New(map[string]string{"addr": addr, "key": "k"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer snk.Close()
	var ce *rediskv.ConnError
	if err := snk.Ping(t.Context()); !errors.As(err, &ce) {
		t.Fatalf("Ping = %v, want a *rediskv.ConnError", err)
	}
	m := message.AcquireMessage()
	defer m.Release()
	message.PopulateFromMap(m, map[string]any{"a": 1})
	if err := snk.Write(t.Context(), m); !errors.As(err, &ce) {
		t.Fatalf("Write = %v, want a *rediskv.ConnError", err)
	}
}
