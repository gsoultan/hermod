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
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The fixtures under testdata are written by testdata/gen_fixtures.py, which
// trains with the hermod-ml worker's own code and records what its scoring
// functions answered. Every comparison here is against those answers.

// tolerance bounds how far a Go score may be from the worker's: both add the
// same float32 values in float64, but numpy may sum a dot product in another
// order.
const tolerance = 1e-5

type fixtureCase struct {
	User        string `json:"user"`
	Item        string `json:"item"`
	K           int    `json:"k"`
	ExcludeSeen bool   `json:"exclude_seen"`
	Items       []Item `json:"items"`
}

func fixture(t testing.TB, algorithm string) (*Model, []fixtureCase) {
	t.Helper()
	dir := filepath.Join("testdata", algorithm)
	raw, err := os.ReadFile(filepath.Join(dir, "model.rec"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s): %v", algorithm, err)
	}
	cj, err := os.ReadFile(filepath.Join(dir, "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []fixtureCase
	if err := json.Unmarshal(cj, &cases); err != nil {
		t.Fatal(err)
	}
	return m, cases
}

func TestServesWhatTheWorkerScored(t *testing.T) {
	for _, algorithm := range []string{AlgorithmALS, AlgorithmItemSimilarity} {
		m, cases := fixture(t, algorithm)
		if m.Algorithm() != algorithm {
			t.Fatalf("fixture %s parsed as %s", algorithm, m.Algorithm())
		}
		for _, c := range cases {
			var got []Item
			var known bool
			if c.User != "" {
				got, known = m.ForUser(c.User, c.K, c.ExcludeSeen)
			} else {
				got, known = m.Similar(c.Item, c.K)
			}
			if known == (c.User == "stranger" || c.Item == "sku-unknown") {
				t.Errorf("%s %+v: known = %v", algorithm, c, known)
			}
			if len(got) != len(c.Items) {
				t.Errorf("%s user %q item %q k=%d: got %s, want %s", algorithm, c.User, c.Item, c.K, show(got), show(c.Items))
				continue
			}
			for i := range got {
				w := c.Items[i]
				if got[i].Item != w.Item || got[i].Reason != w.Reason || math.Abs(got[i].Score-w.Score) > tolerance*math.Max(1, math.Abs(w.Score)) {
					t.Errorf("%s user %q item %q k=%d: rank %d is %+v, want %+v", algorithm, c.User, c.Item, c.K, i, got[i], w)
				}
			}
		}
	}
}
