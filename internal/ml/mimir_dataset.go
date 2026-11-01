// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package ml

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// MimirDatasetRequest says where a dataset goes: the mimir server, the vhost
// secret holding its API key, the name it gets there (the Hermod name when
// empty) and the column a model trained on it will predict (the last column
// when empty).
type MimirDatasetRequest struct {
	URL         string `json:"url"`
	TokenSecret string `json:"token_secret"`
	Name        string `json:"name"`
	Target      string `json:"target"`
}

// maxMimirDatasetBytes bounds the parquet export read into memory before it
// is turned into CSV: the worker's own file limit.
const maxMimirDatasetBytes = 200 << 20

// SendDatasetToMimir copies one of the vhost's datasets to a mimir server as
// a new dataset there, for a model to be trained on it on that side. The
// rows are read from the ML worker as parquet and sent as CSV; the API key
// never leaves the server side. mimir has no append, so each call makes a
// dataset of its own: send again for a fresh copy.
func (s *Service) SendDatasetToMimir(ctx context.Context, vhost, dataset string, req MimirDatasetRequest) (inference.RemoteDataset, error) {
	if strings.TrimSpace(req.URL) == "" {
		return inference.RemoteDataset{}, fmt.Errorf("%w: enter the server's address", ErrBadMimirRequest)
	}
	if !storage.ValidVHostSecretName(req.TokenSecret) {
		return inference.RemoteDataset{}, fmt.Errorf("%w: %q is not the name of a secret", ErrBadMimirRequest, req.TokenSecret)
	}
	w, err := s.Worker()
	if err != nil {
		return inference.RemoteDataset{}, err
	}
	token, err := s.secret(ctx, vhost, req.TokenSecret)
	if err != nil {
		return inference.RemoteDataset{}, fmt.Errorf("%w: the mimir server %w", ErrBadMimirRequest, err)
	}
	export, err := w.ExportDataset(ctx, vhost, dataset)
	if err != nil {
		return inference.RemoteDataset{}, fmt.Errorf("reading dataset %q: %w", dataset, err)
	}
	defer func() { _ = export.Close() }()
	data, err := io.ReadAll(io.LimitReader(export, maxMimirDatasetBytes+1))
	if err != nil {
		return inference.RemoteDataset{}, fmt.Errorf("reading dataset %q: %w", dataset, err)
	}
	if len(data) > maxMimirDatasetBytes {
		return inference.RemoteDataset{}, fmt.Errorf("%w: dataset %q is larger than %d MB", ErrBadMimirRequest, dataset, maxMimirDatasetBytes>>20)
	}
	var csv bytes.Buffer
	if _, err := parquetToCSV(data, &csv); err != nil {
		return inference.RemoteDataset{}, fmt.Errorf("dataset %q: %w", dataset, err)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = dataset
	}
	return s.client.UploadMimirDataset(ctx, strings.TrimSpace(req.URL), token, name, req.Target, &csv)
}
