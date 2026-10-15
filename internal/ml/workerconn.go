// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package ml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

var (
	// ErrWorkerFromEnvironment is returned for a change to the worker
	// connection while HERMOD_ML_WORKER_URL names one, which wins.
	ErrWorkerFromEnvironment = errors.New("the ML worker is set by the server's environment " +
		"(HERMOD_ML_WORKER_URL): change it there")
	// ErrBadWorkerURL is returned for a worker address Hermod cannot call.
	ErrBadWorkerURL = errors.New("invalid ML worker address")
	// ErrSettingsUnsupported is returned when the store keeps no settings.
	ErrSettingsUnsupported = errors.New("this storage backend cannot keep settings")
)

// workerRefreshEvery is how often a process re-reads the saved worker, so one
// saved in another Hermod process sharing the database takes over here too.
// The process that saves it switches at once.
const workerRefreshEvery = 15 * time.Second

// workerReadTimeout bounds one read of the saved worker on the way to a
// prediction or a training.
const workerReadTimeout = 2 * time.Second

// WorkerConnection is the ML worker connected from the console. A zero value
// means none is saved.
type WorkerConnection struct {
	URL   string
	Token string
}

// settings is the part of a store that keeps settings.
type settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SaveSetting(ctx context.Context, key, value string) error
}

// LoadWorkerConnection reads the saved worker, with its token decrypted. A
// token that no longer decrypts (the master key was changed without
// re-encrypting) reads as empty, and is logged by configsecrets.
func LoadWorkerConnection(ctx context.Context, store any) (WorkerConnection, error) {
	st, ok := store.(settings)
	if !ok {
		return WorkerConnection{}, ErrSettingsUnsupported
	}
	raw, err := st.GetSetting(ctx, storage.MLWorkerSettingKey)
	if errors.Is(err, storage.ErrNotFound) || (err == nil && raw == "") {
		return WorkerConnection{}, nil
	}
	if err != nil {
		return WorkerConnection{}, fmt.Errorf("reading the ML worker connection: %w", err)
	}
	var stored map[string]string
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return WorkerConnection{}, fmt.Errorf("the saved ML worker connection is unreadable: %w", err)
	}
	plain := configsecrets.Decrypt(stored)
	return WorkerConnection{URL: plain["url"], Token: plain["token"]}, nil
}

// SaveWorkerConnection saves the worker at rawURL and puts it in force in p
// at once. token nil keeps the saved token, so the address can change without
// typing the token again; an empty token means the worker takes none. An
// empty address disconnects the saved worker. It is refused while the
// environment names a worker.
func SaveWorkerConnection(ctx context.Context, store any, p *worker.Provider, rawURL string, token *string) (WorkerConnection, error) {
	if p.Env() != nil {
		return WorkerConnection{}, ErrWorkerFromEnvironment
	}
	st, ok := store.(settings)
	if !ok {
		return WorkerConnection{}, ErrSettingsUnsupported
	}
	if rawURL == "" {
		if err := st.SaveSetting(ctx, storage.MLWorkerSettingKey, ""); err != nil {
			return WorkerConnection{}, fmt.Errorf("removing the ML worker connection: %w", err)
		}
		p.SetSaved(nil)
		return WorkerConnection{}, nil
	}
	u, err := worker.ValidateURL(rawURL)
	if err != nil {
		return WorkerConnection{}, fmt.Errorf("%w: %w", ErrBadWorkerURL, err)
	}
	conn := WorkerConnection{URL: u}
	if token != nil {
		conn.Token = *token
	} else {
		old, err := LoadWorkerConnection(ctx, store)
		if err != nil {
			return WorkerConnection{}, err
		}
		conn.Token = old.Token
	}

	enc := configsecrets.Encrypt(map[string]string{"url": conn.URL, "token": conn.Token})
	if conn.Token != "" && !configsecrets.IsEncrypted(enc["token"]) {
		// configsecrets drops a value it cannot encrypt rather than store it
		// in the clear; saving without it would quietly lose the token.
		return WorkerConnection{}, errors.New("the ML worker's token could not be encrypted, so it was not saved")
	}
	raw, err := json.Marshal(enc)
	if err != nil {
		return WorkerConnection{}, err
	}
	if err := st.SaveSetting(ctx, storage.MLWorkerSettingKey, string(raw)); err != nil {
		return WorkerConnection{}, fmt.Errorf("saving the ML worker connection: %w", err)
	}
	p.SetSaved(worker.Configure(conn.URL, conn.Token))
	return conn, nil
}

// RefreshWorker re-reads the saved worker into p when it is due (see
// worker.Provider.Refresh). A store without settings, or a failed read,
// leaves p as it is; the failure is logged.
func RefreshWorker(ctx context.Context, store any, p *worker.Provider) {
	if _, ok := store.(settings); !ok {
		return
	}
	err := p.Refresh(ctx, workerRefreshEvery, func(ctx context.Context) (*worker.Client, error) {
		ctx, cancel := context.WithTimeout(ctx, workerReadTimeout)
		defer cancel()
		conn, err := LoadWorkerConnection(ctx, store)
		if err != nil || conn.URL == "" {
			return nil, err
		}
		return worker.Configure(conn.URL, conn.Token), nil
	})
	if err != nil {
		slog.Warn("ml: could not read the saved ML worker; keeping the one in force", "error", err)
	}
}
