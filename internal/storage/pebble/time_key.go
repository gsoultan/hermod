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
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
)

const (
	logKeyPrefix   = "l"
	auditKeyPrefix = "a"

	// legacyTimeKeyCeiling is what the first key scheme subtracted a timestamp
	// from. It is below every nanosecond timestamp since September 2001, so the
	// result was negative and, printed with a sign, sorted oldest first.
	legacyTimeKeyCeiling int64 = 999999999999999999

	// rekeyBatchSize bounds the memory one pass of the rekey holds.
	rekeyBatchSize = 1000
)

// reverseNanos maps a timestamp to a non-negative number that shrinks as the
// timestamp grows, so that printed at a fixed width the newest row sorts first.
func reverseNanos(ts time.Time) int64 {
	return math.MaxInt64 - max(ts.UnixNano(), 0)
}

// timeKey is the key of a log or audit row: <prefix>:<reverse time>:<id>.
func timeKey(prefix string, ts time.Time, id string) []byte {
	return fmt.Appendf(nil, "%s:%020d:%s", prefix, reverseNanos(ts), id)
}

// purgeTimeKeysBefore removes every row of a family whose timestamp is earlier
// than before. Old rows are one contiguous run at the end of the family, so it
// is a single range tombstone: no row is read and none is held in memory.
func (s *pebbleStorage) purgeTimeKeysBefore(prefix string, before time.Time) error {
	rev := reverseNanos(before)
	if rev == math.MaxInt64 {
		return nil // nothing is older than the earliest time a key can hold
	}
	start := fmt.Appendf(nil, "%s:%020d", prefix, rev+1)
	return s.db.DeleteRange(start, []byte(prefix+";"), pebble.Sync)
}

// rekeyLegacyTimeKeys moves the rows of a family written under the first key
// scheme to the current one. Those keys are the only ones whose number starts
// with a minus sign, so a database with none of them costs one empty seek, and
// an interrupted run is finished by the next start.
func (s *pebbleStorage) rekeyLegacyTimeKeys(prefix string) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix + ":-"),
		UpperBound: []byte(prefix + ":."),
	})
	if err != nil {
		return err
	}
	defer func() { _ = iter.Close() }()

	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		ts, id, ok := parseLegacyTimeKey(prefix, string(iter.Key()))
		if !ok {
			continue
		}
		if err := batch.Set(timeKey(prefix, ts, id), iter.Value(), nil); err != nil {
			return err
		}
		if err := batch.Delete(iter.Key(), nil); err != nil {
			return err
		}
		if batch.Count() >= 2*rekeyBatchSize {
			if err := batch.Commit(pebble.Sync); err != nil {
				return err
			}
			batch.Reset()
		}
	}
	if err := iter.Error(); err != nil {
		return err
	}
	if batch.Empty() {
		return nil
	}
	return batch.Commit(pebble.Sync)
}

// parseLegacyTimeKey reads the timestamp and id out of a first-scheme key.
func parseLegacyTimeKey(prefix, key string) (time.Time, string, bool) {
	rest, found := strings.CutPrefix(key, prefix+":")
	if !found {
		return time.Time{}, "", false
	}
	num, id, found := strings.Cut(rest, ":")
	if !found {
		return time.Time{}, "", false
	}
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil || n >= 0 {
		return time.Time{}, "", false
	}
	return time.Unix(0, legacyTimeKeyCeiling-n), id, true
}
