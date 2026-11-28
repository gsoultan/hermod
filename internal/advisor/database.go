// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advisor

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gsoultan/hermod/pkg/infra/dbactivity"
)

// Database card rules.
const (
	// Fixed by Hermod.
	RuleLearnedShare      = "learned_share"
	RuleKeptLookupsMoving = "kept_lookups_moving"
	// Needs a person.
	RuleLongQuery          = "long_query"
	RuleOtherFlows         = "other_flows"
	RulePoolSmallerThanSet = "pool_smaller_than_set"
)

const (
	// longQueryAfter is when a bulk query is long enough to be worth a person
	// looking at: it holds a connection all that time.
	longQueryAfter = 5 * time.Minute
	// recentWait is how long a wait stays worth reporting.
	recentWait = time.Hour
)

// DatabaseUse is one database a flow reaches, as Hermod sees it now.
type DatabaseUse struct {
	SourceID string
	// Source is the name of the Hermod source that reaches it.
	Source   string
	Snapshot dbactivity.Snapshot
}

// Card is one thing said about a database: something Hermod already did, or
// something only a person can change.
type Card struct {
	Rule   string `json:"rule"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	// NodeID is the step of this flow the card is about, if one.
	NodeID   string     `json:"node_id,omitempty"`
	Evidence []Evidence `json:"evidence,omitempty"`
}

// DatabaseView is a database a flow reaches, with what Hermod did about it
// and what needs a person.
type DatabaseView struct {
	SourceID string `json:"source_id"`
	Source   string `json:"source"`
	// Learned is the share of connections Hermod found it gets, 0 for none;
	// Limit the long queries it runs at once, 0 for no bound.
	Learned int `json:"learned"`
	Limit   int `json:"limit"`
	// Running is Hermod's queries here now, from every flow; Long the bulk
	// ones among them.
	Running int `json:"running"`
	Long    int `json:"long"`
	// Fixed is what Hermod did by itself; NeedsYou what only a person can do.
	Fixed    []Card `json:"fixed"`
	NeedsYou []Card `json:"needs_you"`
}

// ReadDatabase says what Hermod did about a database the flow workflowID
// reaches, and what needs a person. names gives a flow's name by its ID.
// A recommendation is the last resort: each one says what Hermod already
// does about it.
func ReadDatabase(u DatabaseUse, workflowID string, names func(string) string, now time.Time) DatabaseView {
	s := u.Snapshot
	v := DatabaseView{SourceID: u.SourceID, Source: u.Source, Learned: s.Learned, Limit: s.Limit,
		Running: s.Running, Long: s.InBulk, Fixed: []Card{}, NeedsYou: []Card{}}
	if c, ok := learnedShare(u); ok {
		v.Fixed = append(v.Fixed, c)
	}
	if c, ok := keptLookupsMoving(u, now); ok {
		v.Fixed = append(v.Fixed, c)
	}
	v.NeedsYou = append(v.NeedsYou, longQueries(u, workflowID, now)...)
	if c, ok := otherFlows(u, workflowID, names, now); ok {
		v.NeedsYou = append(v.NeedsYou, c)
	}
	if c, ok := poolSmallerThanSet(u); ok {
		v.NeedsYou = append(v.NeedsYou, c)
	}
	return v
}

func learnedShare(u DatabaseUse) (Card, bool) {
	s := u.Snapshot
	if s.Learned == 0 {
		return Card{}, false
	}
	return Card{
		Rule:  RuleLearnedShare,
		Title: fmt.Sprintf("Hermod found it gets %d connections on %s", s.Learned, u.Source),
		Detail: fmt.Sprintf("A test query waited while Hermod had %d connections in use there, so that is all it really gets, whatever its own settings say. "+
			"It now runs at most %d long queries at once and keeps %d free for lookups. Every 10 minutes it tries one more, in case more became available.",
			s.Learned, s.Limit, max(s.Learned-s.Limit, 0)),
		Evidence: []Evidence{{Label: "Connections found", Value: strconv.Itoa(s.Learned)}, {Label: "Long queries at once", Value: strconv.Itoa(s.Limit)}},
	}, true
}

func keptLookupsMoving(u DatabaseUse, now time.Time) (Card, bool) {
	s := u.Snapshot
	ago := now.Sub(s.QueuedAt)
	if s.QueuedAt.IsZero() || ago > recentWait || s.Limit == 0 {
		return Card{}, false
	}
	return Card{
		Rule:  RuleKeptLookupsMoving,
		Title: "Lookups kept moving on " + u.Source,
		Detail: fmt.Sprintf("%s ago %s had no connection free. Hermod held new long queries back, so lookups got a connection instead of waiting out their time limit.",
			dur(ago.Round(time.Minute)), u.Source),
	}, true
}

func longQueries(u DatabaseUse, workflowID string, now time.Time) []Card {
	var out []Card
	for _, q := range u.Snapshot.Queries {
		age := now.Sub(q.Started)
		if q.WorkflowID != workflowID || !q.Kind.Bulk() || age < longQueryAfter {
			continue
		}
		out = append(out, Card{
			Rule:   RuleLongQuery,
			NodeID: q.NodeID,
			Title:  fmt.Sprintf("A %s in this flow has run for %s", q.Kind, dur(age.Round(time.Minute))),
			Detail: fmt.Sprintf("It holds a connection to %s for as long as it runs. Hermod keeps lookups moving around it, but it cannot make the query itself faster. "+
				"Open the step's SQL builder and use Speed to see whether it can use an index.", u.Source),
		})
	}
	return out
}

func otherFlows(u DatabaseUse, workflowID string, names func(string) string, now time.Time) (Card, bool) {
	longest := map[string]time.Duration{}
	var order []string
	for _, q := range u.Snapshot.Queries {
		age := now.Sub(q.Started)
		if q.WorkflowID == workflowID || !q.Kind.Bulk() || age < longQueryAfter {
			continue
		}
		if _, seen := longest[q.WorkflowID]; !seen {
			order = append(order, q.WorkflowID)
		}
		longest[q.WorkflowID] = max(longest[q.WorkflowID], age)
	}
	if len(order) == 0 {
		return Card{}, false
	}
	parts := make([]string, 0, len(order))
	for _, id := range order {
		name := names(id)
		if name == "" {
			name = id
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", name, dur(longest[id].Round(time.Minute))))
	}
	return Card{
		Rule:  RuleOtherFlows,
		Title: "Other flows have long queries on " + u.Source,
		Detail: "They share its connections with this flow: " + strings.Join(parts, ", ") +
			". Hermod keeps lookups moving, but long queries take turns. Check those flows' Tuning tabs.",
	}, true
}

func poolSmallerThanSet(u DatabaseUse) (Card, bool) {
	s := u.Snapshot
	if s.Learned == 0 || s.Ceiling == 0 || s.Limit >= s.Ceiling {
		return Card{}, false
	}
	return Card{
		Rule:  RulePoolSmallerThanSet,
		Title: u.Source + " gives Hermod fewer connections than it is set to use",
		Detail: fmt.Sprintf("Hermod is set to run up to %d long queries at once there, but gets only %d connections in all, so it runs %d and the rest wait their turn. "+
			"That is safe, only slower. If they should run together, raise PgBouncer's pool_size for this database and user, or the role's connection limit, above %d.",
			s.Ceiling, s.Learned, s.Limit, s.Learned),
	}, true
}
