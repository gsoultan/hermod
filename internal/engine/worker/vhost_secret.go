// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gsoultan/hermod/internal/storage"
)

// GetVHostSecret reads one secret's value from the control plane. The route
// answers a worker's token and nothing else; see
// AuthHandler.GetVHostSecretForWorker.
func (c *WorkerAPIClient) GetVHostSecret(ctx context.Context, vhost, name string) (storage.VHostSecret, error) {
	path := "/api/worker/vhosts/" + url.PathEscape(vhost) + "/secrets/" + url.PathEscape(name)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return storage.VHostSecret{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return storage.VHostSecret{}, fmt.Errorf("reading secret %q of vhost %q: API error: %s", name, vhost, resp.Status)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return storage.VHostSecret{}, fmt.Errorf("reading secret %q of vhost %q: %w", name, vhost, err)
	}
	return storage.VHostSecret{VHost: vhost, Name: name, Value: body.Value}, nil
}

// A worker reads the secrets its workflows name. Managing them belongs to the
// control plane, which holds the database and the master key; these refuse
// rather than appear to succeed.

func (a *apiStorage) ListVHostSecrets(context.Context, string) ([]storage.VHostSecret, error) {
	return nil, fmt.Errorf("%w: a worker does not list them", storage.ErrVHostSecretsUnsupported)
}

func (a *apiStorage) PutVHostSecret(context.Context, storage.VHostSecret) error {
	return fmt.Errorf("%w: a worker does not save them", storage.ErrVHostSecretsUnsupported)
}

func (a *apiStorage) DeleteVHostSecret(context.Context, string, string) error {
	return fmt.Errorf("%w: a worker does not delete them", storage.ErrVHostSecretsUnsupported)
}

func (a *apiStorage) DeleteVHostSecrets(context.Context, string) error {
	return fmt.Errorf("%w: a worker does not delete them", storage.ErrVHostSecretsUnsupported)
}
