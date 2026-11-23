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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/comm/transformer/core"
)

// defaultDBLookupTimeout is how long one lookup may wait on its database when
// the node does not say.
//
// There used to be no limit at all. A query waiting on PgBouncer's queue for a
// server connection, on a pooled connection whose peer had gone without closing
// it, or on a lock, waited for as long as the message's context allowed -- which
// on a running workflow is until the workflow stops. Nothing completed, the
// engine's stall watchdog (pkg/engine.DefaultStallThreshold, 60s) restarted the
// workflow, and the restart's cancellation surfaced as "failed to execute lookup
// query: context canceled": an error that named neither the database nor the
// wait, on every message that was in flight.
//
// 30s sits under that threshold, so a stuck lookup fails as itself and the
// message takes the node's error path (dead letter, onError) instead of the
// whole workflow being torn down over it. A lookup is a per-message point read;
// one that legitimately runs longer can say so with the node's timeout. A
// variable so tests can shorten it.
var defaultDBLookupTimeout = 30 * time.Second

// resolveLookupTimeout is the node's time limit: its timeout setting, or the
// default when the field is empty. A value that is not a positive duration is
// an error rather than "no limit", so a typo cannot quietly bring the unbounded
// wait back.
func resolveLookupTimeout(config map[string]any) (time.Duration, error) {
	s := strings.TrimSpace(core.GetConfigString(config, "timeout"))
	if s == "" {
		return defaultDBLookupTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("db_lookup: timeout %q is not a positive duration such as %q", s, "30s")
	}
	return d, nil
}

// lookupTimeoutError says that the lookup ran out of its own time, when it did.
//
// Only then: parent is the message's context, and when that one ended first --
// a stop, a drain, a preview the editor abandoned -- the database did nothing
// wrong and the error is passed on as it is, so the traversal still recognises
// it as an interruption.
func lookupTimeoutError(parent context.Context, timeout time.Duration, err error) error {
	if err == nil || parent.Err() != nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("db_lookup: no answer from the database within %s; "+
		"raise the step's time limit if the query is legitimately slow, "+
		"or check for clients waiting on the pool (PgBouncer: SHOW POOLS, cl_waiting): %w", timeout, err)
}
