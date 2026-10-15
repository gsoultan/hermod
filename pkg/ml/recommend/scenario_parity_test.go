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
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The fixtures under testdata/scenario are written by
// testdata/gen_scenario_fixtures.py, which trains each scenario with the
// worker's own code and records what its scoring functions answered.

type scenarioCase struct {
	Today       string `json:"today"`
	Salesman    string `json:"salesman"`
	Customer    string `json:"customer"`
	Product     string `json:"product"`
	K           int    `json:"k"`
	ExcludeSeen bool   `json:"exclude_seen"`
	Items       []Pick `json:"items"`
	Summary     string `json:"summary"`
}

func scenarioFixture(t testing.TB, name string) (*Model, []scenarioCase) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "scenario", name+".rec"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	cj, err := os.ReadFile(filepath.Join("testdata", "scenario", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []scenarioCase
	if err := json.Unmarshal(cj, &cases); err != nil {
		t.Fatal(err)
	}
	return m, cases
}

func samePick(got, want Pick) error {
	if got.ID != want.ID || got.Rank != want.Rank || got.DueDate != want.DueDate {
		return fmt.Errorf("got %s #%d due %q, want %s #%d due %q", got.ID, got.Rank, got.DueDate, want.ID, want.Rank, want.DueDate)
	}
	if math.Abs(got.Score-want.Score) > tolerance*math.Max(1, math.Abs(want.Score)) {
		return fmt.Errorf("%s: score %v, want %v", got.ID, got.Score, want.Score)
	}
	g, _ := json.Marshal(got.Reasons)
	w, _ := json.Marshal(want.Reasons)
	if string(g) != string(w) {
		return fmt.Errorf("%s: reasons %s, want %s", got.ID, g, w)
	}
	g, _ = json.Marshal(got.Fields)
	w, _ = json.Marshal(want.Fields)
	if string(g) != string(w) {
		return fmt.Errorf("%s: fields %s, want %s", got.ID, g, w)
	}
	return nil
}

func TestScenariosServeWhatTheWorkerScored(t *testing.T) {
	// The "_peers", "_cold" and "_near" models were trained with customer
	// categories and sizes, so products and audience add the peer share,
	// from the nearby peers when there are enough; the "_details" and
	// "_shop" models with products' details, so they weigh brands and new
	// products, leave out what cannot be offered for its substitute and
	// suggest quantities; the "_market" models with customers' price tiers,
	// channels and regions, so peers are of all of them; the "_signals"
	// models with returns and quotes, so what was returned or lost is left
	// out and an open quote offered; the "_industrial" and "_pharmacy"
	// models on two of the recommendation cases (docs/ml-recommendation-cases.md);
	// the others were trained before there was any of it, and answer as they
	// always did.
	for _, name := range []string{"visits", "products", "products_als", "reorder", "audience", "audience_als", "similar",
		"products_peers", "audience_peers", "audience_peers_als", "reorder_peers", "products_cold", "audience_cold",
		"products_peers_wide", "products_near", "audience_near", "products_details", "audience_details", "similar_details",
		"products_shop", "audience_shop", "similar_shop", "products_market", "audience_market", "products_signals",
		"audience_signals", "products_industrial", "audience_industrial", "reorder_industrial", "products_pharmacy",
		"similar_pharmacy"} {
		t.Run(name, func(t *testing.T) {
			m, cases := scenarioFixture(t, name)
			if len(cases) == 0 {
				t.Fatal("no cases")
			}
			for _, c := range cases {
				today, err := time.Parse(time.DateOnly, c.Today)
				if err != nil {
					t.Fatal(err)
				}
				q := Query{Today: today, Salesman: c.Salesman, Customer: c.Customer, Product: c.Product, K: c.K, ExcludeSeen: c.ExcludeSeen}
				if m.Scenario() == ScenarioReorder && c.Salesman != "" {
					q.K = MaxPlan
				}
				res, err := m.Answer(q)
				if err != nil {
					t.Fatalf("%+v: %v", q, err)
				}
				if len(res) != 1 {
					t.Fatalf("%d results", len(res))
				}
				if res[0].Summary != c.Summary {
					t.Errorf("%s %s: summary %q, want %q", name, c.Customer, res[0].Summary, c.Summary)
				}
				got := res[0].Items
				want := c.Items
				// A visit plan lists what fits in the period's capacity: the
				// best of the worker's ranking.
				if m.Scenario() == ScenarioVisits {
					if len(got) == 0 || len(got) > len(want) {
						t.Fatalf("salesman %s: %d planned of %d", c.Salesman, len(got), len(want))
					}
					want = want[:len(got)]
				}
				if len(got) != len(want) {
					t.Fatalf("%s %s%s%s: %d items, want %d", name, c.Salesman, c.Customer, c.Product, len(got), len(want))
				}
				for i := range got {
					if err := samePick(got[i], want[i]); err != nil {
						t.Errorf("%s %s%s%s: rank %d: %v", name, c.Salesman, c.Customer, c.Product, i+1, err)
					}
				}
			}
		})
	}
}
