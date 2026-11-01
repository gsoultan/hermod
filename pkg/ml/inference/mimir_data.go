// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// RemoteDataset is a dataset as a mimir server holds it after an upload:
// enough to find it on the server and to see whether it can be learned from.
type RemoteDataset struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Rows    int64          `json:"rows"`
	Target  string         `json:"target,omitempty"`
	Columns []RemoteColumn `json:"columns,omitempty"`
	// Verdict is the server's check of the data: "good", "warn" or "bad"
	// (bad means it cannot learn from it yet). Empty when the server gave
	// none.
	Verdict string `json:"verdict,omitempty"`
}

// uploadTimeout bounds one dataset upload: a CSV of a million rows takes a
// while to send and for the server to read.
const uploadTimeout = 10 * time.Minute

// UploadMimirDataset sends a CSV, with a header row, to the mimir server at
// url as a new dataset called name, for a model to be trained on there.
// target is the column to predict; empty means the last one. mimir has no
// append: each upload is a dataset of its own.
func (c *Client) UploadMimirDataset(ctx context.Context, url, token, name, target string, csv io.Reader) (RemoteDataset, error) {
	if strings.TrimSpace(name) == "" {
		return RemoteDataset{}, errors.New("the dataset needs a name on the mimir server")
	}
	client, err := c.mimirClient(url, token)
	if err != nil {
		return RemoteDataset{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	res, err := client.Datasets.UploadCSV(ctx, strings.TrimSpace(name), strings.TrimSpace(target), csv)
	if err != nil {
		return RemoteDataset{}, fmt.Errorf("the mimir server did not take the dataset: %w", err)
	}
	ds := res.GetDataset()
	out := RemoteDataset{
		ID: ds.GetId(), Name: ds.GetName(), Rows: ds.GetRows(), Target: ds.GetTarget(), Verdict: res.GetCheck().GetVerdict(),
	}
	for _, col := range ds.GetColumns() {
		out.Columns = append(out.Columns, RemoteColumn{Name: col.GetName(), Kind: col.GetKind()})
	}
	return out, nil
}
