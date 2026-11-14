// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package pebble

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gsoultan/hermod/internal/storage"
)

// PurgeAuditLogsForVHost deletes one vhost's audit rows older than before.
//
// The vhost and the time are read from each value. The key holds a time too,
// but not one this can range over: it is 999999999999999999 minus the Unix
// nanoseconds, which has been negative since 2001, so keys sort by the
// magnitude of a negative number rather than newest first.
func (s *pebbleStorage) PurgeAuditLogsForVHost(ctx context.Context, vhost string, before time.Time) error {
	if vhost == "" {
		return nil
	}
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("a:"),
		UpperBound: []byte("b:"),
	})
	if err != nil {
		return err
	}
	defer func() { _ = iter.Close() }()

	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var a storage.AuditLog
		if err := json.Unmarshal(iter.Value(), &a); err != nil || a.VHost != vhost || !a.Timestamp.Before(before) {
			continue
		}
		if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}
