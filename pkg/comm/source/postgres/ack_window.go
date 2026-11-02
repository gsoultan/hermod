// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package postgres

import (
	"container/heap"

	"github.com/jackc/pglogrepl"
)

// ackWindow turns out-of-order acknowledgements into the position that may be
// confirmed to the slot: just below the lowest change still unacknowledged, or
// the highest change handed over when none is.
//
// Changes are handed over in WAL order by the one replication goroutine, but
// the engine processes them on a pool of workers and acknowledges each as it
// finishes. Keeping only the highest acknowledgement, as Ack did, confirmed
// every slower change below it, and a stop or crash then lost them.
//
// Not pkg/infra/ackwatermark, which polling sources use: it re-indexes its
// queue on every acknowledgement and keeps every entry behind an
// unacknowledged one, which on a CDC stream is per-change work and, behind
// one message that is never acknowledged, memory without bound. This keeps
// only what is outstanding.
//
// Counts rather than a set, so the same position handed over twice (a replay
// after a reconnect) needs two acknowledgements. An acknowledgement may land
// before emit records the handover, because the channel send comes first; it
// is held in early until it does. The caller holds PostgresSource.mu.
type ackWindow struct {
	outstanding map[pglogrepl.LSN]int
	early       map[pglogrepl.LSN]int
	// low holds every outstanding position, and may hold some that are no
	// longer: they are discarded when they reach the top.
	low        lsnHeap
	maxEmitted pglogrepl.LSN
	safe       pglogrepl.LSN
}

// emit records a change handed to the consumer.
func (w *ackWindow) emit(lsn pglogrepl.LSN) {
	w.maxEmitted = max(w.maxEmitted, lsn)
	if n := w.early[lsn]; n > 0 {
		if n == 1 {
			delete(w.early, lsn)
		} else {
			w.early[lsn] = n - 1
		}
	} else {
		if w.outstanding == nil {
			w.outstanding = make(map[pglogrepl.LSN]int)
		}
		if w.outstanding[lsn] == 0 {
			heap.Push(&w.low, lsn)
		}
		w.outstanding[lsn]++
	}
	w.advance()
}

// ack records one acknowledgement.
func (w *ackWindow) ack(lsn pglogrepl.LSN) {
	switch n := w.outstanding[lsn]; n {
	case 0:
		if w.early == nil {
			w.early = make(map[pglogrepl.LSN]int)
		}
		w.early[lsn]++
	case 1:
		delete(w.outstanding, lsn)
	default:
		w.outstanding[lsn] = n - 1
	}
	w.advance()
}

func (w *ackWindow) advance() {
	for len(w.low) > 0 && w.outstanding[w.low[0]] == 0 {
		heap.Pop(&w.low)
	}
	candidate := w.maxEmitted
	if len(w.low) > 0 {
		// Every change below the lowest outstanding one is acknowledged. Any
		// position under it is safe to confirm: the transaction holding the
		// outstanding change commits above it, so the server sends it again.
		candidate = w.low[0] - 1
	}
	w.safe = max(w.safe, candidate)
	// Positions acknowledged while a lower one was outstanding stay in the
	// heap until they surface. Rebuild before they outnumber the live ones.
	if len(w.low) > 2*len(w.outstanding)+1024 {
		w.low = w.low[:0]
		for lsn := range w.outstanding {
			w.low = append(w.low, lsn)
		}
		heap.Init(&w.low)
	}
}

// pending reports whether a delivered change is still unacknowledged.
func (w *ackWindow) pending() bool { return len(w.outstanding) > 0 }

func (w *ackWindow) reset() { *w = ackWindow{} }

type lsnHeap []pglogrepl.LSN

func (h lsnHeap) Len() int           { return len(h) }
func (h lsnHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h lsnHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *lsnHeap) Push(x any) {
	lsn, _ := x.(pglogrepl.LSN) // only ever pushed an LSN, by emit
	*h = append(*h, lsn)
}
func (h *lsnHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
