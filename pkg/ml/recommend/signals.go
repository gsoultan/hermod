// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package recommend

import (
	"cmp"
	"slices"
)

// Returns and quotes (the worker's _signals): what a customer sent back,
// and the quotes it lost or has not ordered yet. A model trained without
// them, or before them, answers as it did.

// The kinds of a signal, as the worker's RETURNED, LOST and QUOTED.
const (
	signalReturned = 0
	signalLost     = 1
	signalQuoted   = 2
)

// ReasonQuoted is the reason of a product offered for its open quote.
const ReasonQuoted = "quoted"

// As the worker's RETURNED_AUDIENCE, QUOTE_DAYS, QUOTE_BOOST and
// QUOTE_CANDIDATES: what a returned product's audience score is multiplied
// by, the days a quote counts for, what an open quote adds to a product's
// score, and how many open quotes a customer's products also take.
const (
	returnedAudience = 0.5
	quoteDays        = 90
	quoteBoost       = 0.1
	quoteCandidates  = 10
)

// reasonQuote marks a candidate taken for its open quote.
const reasonQuote = "quoted"

// pairSignal is one signal: a customer, an item, its day (the last return,
// or the quote) and its kind.
type pairSignal struct{ u, i, day, kind uint32 }

// readSignals reads the four signal sections.
func readSignals(r *reader, n int64) []pairSignal {
	users := r.uints(n, "signal customers")
	items := r.uints(n, "signal items")
	days := r.uints(n, "signal days")
	kinds := r.uints(n, "signal kinds")
	if r.err != nil {
		return nil
	}
	out := make([]pairSignal, n)
	for k := range out {
		out[k] = pairSignal{users[k], items[k], days[k], kinds[k]}
	}
	return out
}

func compareSignals(a, b pairSignal) int {
	if c := cmp.Compare(a.u, b.u); c != 0 {
		return c
	}
	if c := cmp.Compare(a.i, b.i); c != 0 {
		return c
	}
	return cmp.Compare(a.kind, b.kind)
}

// checkSignals checks each signal is of a customer and item of the model
// and of a known kind, strictly ascending.
func (m *Model) checkSignals() error {
	nUsers, nItems := uint32(len(m.users)), uint32(len(m.items))
	for k, s := range m.sc.signals {
		switch {
		case s.u >= nUsers:
			return invalid("a signal is of customer %d of %d customers", s.u, nUsers)
		case s.i >= nItems:
			return invalid("a signal is of item %d of %d items", s.i, nItems)
		case s.kind > signalQuoted:
			return invalid("a signal is of kind %d", s.kind)
		case k > 0 && compareSignals(m.sc.signals[k-1], s) >= 0:
			return invalid("the signals are not strictly ascending")
		}
	}
	return nil
}

// signalsOf is customer u's signals.
func (m *Model) signalsOf(u int) []pairSignal {
	if m.sc == nil || len(m.sc.signals) == 0 {
		return nil
	}
	list := m.sc.signals
	from, _ := slices.BinarySearchFunc(list, uint32(u), func(s pairSignal, t uint32) int { return cmp.Compare(s.u, t) })
	to := from
	for to < len(list) && list[to].u == uint32(u) {
		to++
	}
	return list[from:to]
}

// returned reports whether customer u sent back enough of item i that it
// is not offered again, as the worker's returned.
func (c *call) returned(u, i int) bool {
	for _, s := range c.m.signalsOf(u) {
		if s.i == uint32(i) && s.kind == signalReturned {
			return true
		}
	}
	return false
}

// quote is customer u's quote of item i within quoteDays days before
// today, as the worker's quote: its kind (signalLost or signalQuoted) and
// day; ok is false for none.
func (c *call) quote(u, i int) (kind uint32, day int, ok bool) {
	for _, s := range c.m.signalsOf(u) {
		if s.i != uint32(i) || s.kind == signalReturned {
			continue
		}
		if age := c.today - int(s.day); age >= 0 && age < quoteDays {
			return s.kind, int(s.day), true
		}
	}
	return 0, 0, false
}

// lostQuote reports whether customer u lost a quote of item i within
// quoteDays days.
func (c *call) lostQuote(u, i int) bool {
	kind, _, ok := c.quote(u, i)
	return ok && kind == signalLost
}

// openQuote is the day of customer u's open quote of item i within
// quoteDays days; ok is false for none.
func (c *call) openQuote(u, i int) (int, bool) {
	kind, day, ok := c.quote(u, i)
	return day, ok && kind == signalQuoted
}

// heldBack are the items never offered to customer u, as the worker's
// held_back: those it returned, and those of a quote it lost; nil for
// none.
func (c *call) heldBack(u int) map[int]bool {
	var out map[int]bool
	for _, s := range c.m.signalsOf(u) {
		i := int(s.i)
		if c.returned(u, i) || c.lostQuote(u, i) {
			if out == nil {
				out = map[int]bool{}
			}
			out[i] = true
		}
	}
	return out
}

// quoteCandidatesOf are customer u's open quotes not taken, latest first,
// as the worker's _quote_candidates; quoteCandidates at most.
func (c *call) quoteCandidatesOf(u int, taken map[int]bool) []candidate {
	type open struct{ day, item int }
	var list []open
	for _, s := range c.m.signalsOf(u) {
		i := int(s.i)
		if taken[i] || s.kind != signalQuoted {
			continue
		}
		if day, ok := c.openQuote(u, i); ok {
			list = append(list, open{day, i})
		}
	}
	slices.SortFunc(list, func(a, b open) int {
		if a.day != b.day {
			return b.day - a.day
		}
		return a.item - b.item
	})
	out := make([]candidate, 0, min(len(list), quoteCandidates))
	for _, o := range list[:min(len(list), quoteCandidates)] {
		out = append(out, candidate{item: o.item, reason: reasonQuote})
	}
	return out
}

func quoteReasonOf(day int) Reason {
	return Reason{Code: ReasonQuoted, Text: "Quoted on " + dayText(day) + ", not ordered yet"}
}

// withHeldBack is mine (ascending) and the items held back, ascending.
func withHeldBack(mine []uint32, held map[int]bool) []uint32 {
	if len(held) == 0 {
		return mine
	}
	out := slices.Clone(mine)
	for i := range held {
		out = append(out, uint32(i))
	}
	slices.Sort(out)
	return slices.Compact(out)
}
