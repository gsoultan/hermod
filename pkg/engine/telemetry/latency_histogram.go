// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package telemetry

import (
	"math"
	"math/bits"
	"sync/atomic"
	"time"
)

// latencyBuckets is how many buckets a node's run-time histogram has. Bucket
// k counts runs that took more than 2^(k-1) and at most 2^k microseconds, so
// the buckets run from 1µs to 8.4s; the last also takes everything slower.
const latencyBuckets = 24

// LatencyHistogram is a reading of one node's run times, by bucket.
//
// A moving average hides a step that is usually fast and sometimes very
// slow; the histogram is what shows it. Doubling buckets place a quantile
// within a factor of two, which is enough to tell 5ms from 500ms and costs
// one atomic add per run.
type LatencyHistogram [latencyBuckets]uint64

// latencyBucket is the bucket a run of length d falls in.
func latencyBucket(d time.Duration) int {
	us := (int64(d) + 999) / 1000
	if us <= 1 {
		return 0
	}
	return min(bits.Len64(uint64(us-1)), latencyBuckets-1)
}

// Total is how many runs the histogram holds.
func (h LatencyHistogram) Total() uint64 {
	var n uint64
	for _, c := range h {
		n += c
	}
	return n
}

// Quantile returns the upper bound of the bucket that the q-th quantile of
// the runs falls in: q of them took no longer than this. Zero when empty.
func (h LatencyHistogram) Quantile(q float64) time.Duration {
	total := h.Total()
	if total == 0 {
		return 0
	}
	target := uint64(math.Ceil(q * float64(total)))
	target = min(max(target, 1), total)
	var seen uint64
	for k, c := range h {
		seen += c
		if seen >= target {
			return time.Duration(1<<k) * time.Microsecond
		}
	}
	return time.Duration(1<<(latencyBuckets-1)) * time.Microsecond
}

// Since returns the runs counted after an earlier reading of the same node.
// The counters only grow, so if any went back the engine restarted and h is
// returned as it is.
func (h LatencyHistogram) Since(earlier LatencyHistogram) LatencyHistogram {
	var d LatencyHistogram
	for k := range h {
		if h[k] < earlier[k] {
			return h
		}
		d[k] = h[k] - earlier[k]
	}
	return d
}

// latencyCounters is the live side of a LatencyHistogram.
type latencyCounters [latencyBuckets]atomic.Uint64

func (c *latencyCounters) read() LatencyHistogram {
	var h LatencyHistogram
	for k := range c {
		h[k] = c[k].Load()
	}
	return h
}

// NodeLatencyHistograms returns each node's run-time histogram since the
// engine started. Nodes that have not run are absent.
func (s *StatusTracker) NodeLatencyHistograms() map[string]LatencyHistogram {
	res := make(map[string]LatencyHistogram)
	s.nodeStats.Range(func(key, value any) bool {
		id, okKey := key.(string)
		n, okVal := value.(*NodeStats)
		if okKey && okVal {
			if h := n.histogram.read(); h.Total() > 0 {
				res[id] = h
			}
		}
		return true
	})
	return res
}
