// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package monitor

import "time"

// A recommendation model has no feature to drift: it is asked for a person or
// an item. It is watched instead by what it answers, over the same tumbling
// windows as drift:
//
//   - coverage: the share of its catalogue it recommended at least once. A
//     model that shows everyone the same few items has low coverage.
//   - popular share: the share of calls about a person or item it did not
//     know, answered with the most popular items. A rising share means new
//     people or items arrive that the model was not trained on; train it
//     again.

// RecommendationCall is one call of a recommendation model, for Observation.
type RecommendationCall struct {
	// Items are the ids it answered.
	Items []string
	// Fallback reports that the person or item was unknown, so the answer is
	// the popular items.
	Fallback bool
	// Catalog is how many items the model can recommend.
	Catalog int
}

// RecommendationReport is one window of a recommendation model's calls.
type RecommendationReport struct {
	VHost       string    `json:"vhost"`
	Model       string    `json:"model"`
	Version     string    `json:"version"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	// Calls is how many calls the window holds; Fallbacks how many were
	// answered with popular items.
	Calls     int64 `json:"calls"`
	Fallbacks int64 `json:"fallbacks"`
	// PopularShare is Fallbacks / Calls.
	PopularShare float64 `json:"popular_share"`
	// Recommended is how many distinct items were recommended, of Catalog;
	// Coverage is the share.
	Recommended int     `json:"recommended"`
	Catalog     int     `json:"catalog"`
	Coverage    float64 `json:"coverage"`
}

// recWindow counts one recommendation model version's calls until judged.
type recWindow struct {
	version   string
	start     time.Time
	calls     int64
	fallbacks int64
	catalog   int
	// items is every distinct item recommended. It holds at most the
	// catalogue, which the artifact bounds.
	items map[string]struct{}
}

// recEvent is a RecommendationCall copied for the monitor's goroutine.
type recEvent struct {
	items    []string
	fallback bool
	catalog  int
}

// countRecommendation adds a call to its model's window, starting a new
// window when the live version changed.
func (m *Monitor) countRecommendation(ev event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.recWindows[ev.key]
	if w == nil || w.version != ev.version {
		w = &recWindow{version: ev.version, start: m.now(), items: map[string]struct{}{}}
		m.recWindows[ev.key] = w
	}
	w.calls++
	if ev.rec.fallback {
		w.fallbacks++
	}
	w.catalog = ev.rec.catalog
	for _, it := range ev.rec.items {
		if len(w.items) >= w.catalog && w.catalog > 0 {
			break
		}
		w.items[it] = struct{}{}
	}
}

// judgeRecommendations reports every window holding at least MinRows calls
// and starts a new one. It runs with m.mu held.
func (m *Monitor) judgeRecommendations(now time.Time) []RecommendationReport {
	var out []RecommendationReport
	for k, w := range m.recWindows {
		if w.calls < int64(m.cfg.MinRows) {
			continue
		}
		r := RecommendationReport{
			VHost: k.vhost, Model: k.model, Version: w.version, WindowStart: w.start, WindowEnd: now,
			Calls: w.calls, Fallbacks: w.fallbacks, PopularShare: float64(w.fallbacks) / float64(w.calls),
			Recommended: len(w.items), Catalog: w.catalog,
		}
		if w.catalog > 0 {
			r.Coverage = float64(len(w.items)) / float64(w.catalog)
		}
		m.recReports[k] = r
		m.recWindows[k] = &recWindow{version: w.version, start: now, items: map[string]struct{}{}}
		out = append(out, r)
	}
	return out
}

// Recommendations returns the latest report of a recommendation model.
func (m *Monitor) Recommendations(vhost, model string) (RecommendationReport, bool) {
	if m == nil {
		return RecommendationReport{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.recReports[modelKey{vhost, model}]
	return r, ok
}
