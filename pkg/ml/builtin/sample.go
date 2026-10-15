// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package builtin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gsoultan/hermod/pkg/ml/builtin/table"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// sampleRowsFor is how many rows a sample of a dataset with cols columns
// takes when asked for rows: at most worker.SampleMaxRows, and fewer for a
// wide dataset, so it holds at most worker.SampleMaxCells values.
func sampleRowsFor(rows, cols int) int {
	rows = min(max(rows, 0), worker.SampleMaxRows)
	if cols > 0 {
		rows = min(rows, worker.SampleMaxCells/cols)
	}
	return rows
}

// sampleDataset answers rows of a dataset spread evenly over it, for its
// profile (pkg/ml/profile): row i*n/k of the n rows, for i below k.
func (e *Engine) sampleDataset(r *http.Request) (any, error) {
	vhost, name := r.PathValue("vhost"), r.PathValue("name")
	want, err := strconv.Atoi(r.URL.Query().Get("rows"))
	if err != nil || want <= 0 {
		return nil, badRequest("Query parameter 'rows' must be a whole number above 0.")
	}
	info, err := e.datasets.info(r.Context(), vhost, name)
	if err != nil {
		return nil, err
	}
	t, err := e.datasets.load(r.Context(), vhost, name, 0)
	if err != nil {
		return nil, err
	}
	k := min(sampleRowsFor(want, len(info.Columns)), t.Rows)
	for {
		s := sampleOf(info, t, k)
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		// Long texts can make a sample too big to send: halve it until it fits.
		if len(raw) <= worker.SampleMaxBytes || k <= 1 {
			return json.RawMessage(raw), nil
		}
		k /= 2
	}
}

// sampleOf takes k rows of t, spread evenly, with text cut to
// worker.SampleMaxText characters.
func sampleOf(info worker.DatasetInfo, t *table.Table, k int) worker.DatasetSample {
	s := worker.DatasetSample{Name: info.Name, Rows: t.Rows, Columns: info.Columns, SampleRows: k, Values: map[string][]any{}}
	idx := make([]int, k)
	for i := range idx {
		idx[i] = int(int64(i) * int64(t.Rows) / int64(k))
	}
	for _, c := range t.Columns {
		vals := make([]any, k)
		for i, row := range idx {
			v := c.Value(row)
			if str, ok := v.(string); ok {
				v = cut(str, worker.SampleMaxText)
			}
			vals[i] = v
		}
		s.Values[c.Name] = vals
	}
	return s
}

// cut is s with at most n characters.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := 0
	for at := range s {
		if i == n {
			return s[:at]
		}
		i++
	}
	return s
}
