// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package lookup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

// --- "no answer from the database within 30s" did not say where it waited ---
//
// Reported with ~1000 workflows on one database through PgBouncer. The message
// pointed at PgBouncer's cl_waiting, but a lookup can also spend its whole time
// limit inside Hermod, queued for one of the source's pooled connections, and
// that queue does not show in SHOW POOLS at all. The error now says which of
// the two it was, from the pool's own counters at the moment it gave up.

func TestDBLookupTimeoutSaysWhenHermodsPoolWasFull(t *testing.T) {
	reg, d := hangingLookupRegistry(t)
	reg.db.SetMaxOpenConns(1)

	// One lookup takes the only connection and holds it.
	holder, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)
	held := make(chan struct{})
	go func() {
		defer close(held)
		msg := message.AcquireMessage()
		defer msg.Release()
		msg.SetData("id", 1)
		_, _ = (&DBLookupTransformer{}).Transform(lookupCtx(holder, reg), msg, tableLookupConfig(map[string]any{"timeout": "1m"}))
	}()
	t.Cleanup(func() { stop(); <-held })
	waitFor(t, func() bool { return d.queries.Load() == 1 })

	err := runHungLookup(lookupCtx(t.Context(), reg), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if err == nil {
		t.Fatal("a lookup with no free connection succeeded")
	}
	if !strings.Contains(err.Error(), "all 1 of Hermod's connections to this source were busy") {
		t.Errorf("error does not say the wait was in Hermod's own pool:\n%v", err)
	}
}

func TestDBLookupTimeoutSaysWhenTheDatabaseDidNotAnswer(t *testing.T) {
	reg, _ := hangingLookupRegistry(t)
	reg.db.SetMaxOpenConns(5)

	err := runHungLookup(lookupCtx(t.Context(), reg), t, tableLookupConfig(map[string]any{"timeout": "100ms"}))
	if err == nil {
		t.Fatal("a query that never answered succeeded")
	}
	if !strings.Contains(err.Error(), "0 of Hermod's 5 connections to this source were busy, so the wait was in the database or PgBouncer") {
		t.Errorf("error does not say Hermod's pool had room:\n%v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func lookupCtx(ctx context.Context, reg *fullFakeRegistry) context.Context {
	return context.WithValue(ctx, hermod.RegistryKey, reg)
}
