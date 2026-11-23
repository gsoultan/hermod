// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package agenthub

import "slices"

// ledger is what Hermod owes one agent: the records it accepted and which
// workflows still have to finish each one. The agent's acknowledgements are
// cumulative, so the ack only moves past a record once every record before it
// is finished too.
//
// It belongs to the agent, not to one stream, so a reconnect does not hand
// the workflows records they already hold.
type ledger struct {
	// acked is the highest sequence number acknowledged to the agent.
	acked uint64
	// highest is the highest sequence number accepted. The agent sends in
	// order, so anything at or below it is a resend.
	highest uint64
	// queue holds the accepted records not yet acknowledged, in order.
	queue []*entry
}

type entry struct {
	seq     uint64
	waiting []*Subscription
}

// accept records seq as owed to subs and reports false for a resend.
func (l *ledger) accept(seq uint64, subs []*Subscription) bool {
	if seq <= l.highest {
		return false
	}
	l.highest = seq
	l.queue = append(l.queue, &entry{seq: seq, waiting: slices.Clone(subs)})
	return true
}

// done records that sub has finished seq, and reports whether the ack moved.
func (l *ledger) done(sub *Subscription, seq uint64) bool {
	i, found := slices.BinarySearchFunc(l.queue, seq, func(e *entry, s uint64) int {
		switch {
		case e.seq < s:
			return -1
		case e.seq > s:
			return 1
		}
		return 0
	})
	if !found {
		return false
	}
	e := l.queue[i]
	e.waiting = slices.DeleteFunc(e.waiting, func(s *Subscription) bool { return s == sub })
	return l.advance()
}

// advance moves the ack past every finished record at the head.
func (l *ledger) advance() bool {
	n := 0
	for n < len(l.queue) && len(l.queue[n].waiting) == 0 {
		l.acked = l.queue[n].seq
		n++
	}
	if n == 0 {
		return false
	}
	l.queue = slices.Delete(l.queue, 0, n)
	return true
}

// holds reports whether sub still has records to finish.
func (l *ledger) holds(sub *Subscription) bool {
	for _, e := range l.queue {
		if slices.Contains(e.waiting, sub) {
			return true
		}
	}
	return false
}

// reset forgets everything not acknowledged, so the agent's resend of it is
// accepted as new, and takes acked as the agent's ack.
func (l *ledger) reset(acked uint64) {
	l.acked, l.highest, l.queue = acked, acked, nil
}
