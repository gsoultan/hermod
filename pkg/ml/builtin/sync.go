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
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// syncChunkRows is how many rows one call copies to the worker.
const syncChunkRows = 2000

// syncState remembers which state of each dataset the worker last got, so
// an unchanged dataset is not copied again for every training.
type syncState struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
	sent  map[string]string
}

func syncKey(remote *worker.Client, vhost, name string) string {
	return remote.URL + "\x00" + vhost + "\x00" + name
}

// lock serialises the copies of one dataset to one worker.
func (s *syncState) lock(key string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks, s.sent = map[string]*sync.Mutex{}, map[string]string{}
	}
	l, ok := s.locks[key]
	if !ok {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *syncState) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sent[key]
}

func (s *syncState) set(key, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent == nil {
		s.locks, s.sent = map[string]*sync.Mutex{}, map[string]string{}
	}
	if state == "" {
		delete(s.sent, key)
		return
	}
	s.sent[key] = state
}

// forgetSync forgets what the worker got of the dataset, so it is copied
// again the next time it trains on it.
func (e *Engine) forgetSync(remote *worker.Client, vhost, name string) {
	e.synced.set(syncKey(remote, vhost, name), "")
}

// state names the dataset's content: its generation and its parts. It
// changes whenever rows are added or the dataset is replaced, and is "" for
// no such dataset.
func (s *datasetStore) state(ctx context.Context, vhost, name string) (string, int, error) {
	gen, err := s.head(ctx, vhost, name)
	if err != nil || gen == "" {
		return "", 0, err
	}
	parts, _, err := s.parts(ctx, vhost, name, gen)
	if err != nil {
		return "", 0, err
	}
	state := gen + "/" + strconv.Itoa(len(parts))
	if len(parts) > 0 {
		state += "/" + parts[len(parts)-1].ID
	}
	return state, rowsOf(parts), nil
}

// syncDataset copies the engine's dataset to the worker, replacing the
// worker's copy, unless the worker already has this state of it. A dataset
// the engine does not have is left to the worker.
func (e *Engine) syncDataset(ctx context.Context, remote *worker.Client, vhost, name string) error {
	if err := checkDatasetName(vhost, name); err != nil {
		return err
	}
	key := syncKey(remote, vhost, name)
	defer e.synced.lock(key)()
	state, _, err := e.datasets.state(ctx, vhost, name)
	if err != nil || state == "" || state == e.synced.get(key) {
		return err
	}
	t, err := e.datasets.load(ctx, vhost, name, 0)
	if err != nil {
		return err
	}
	total := 0
	for from := 0; from < t.Rows || from == 0; from += syncChunkRows {
		total, err = remote.AppendRows(ctx, vhost, name, t.Records(from, min(from+syncChunkRows, t.Rows)), from == 0)
		if err != nil {
			e.synced.set(key, "")
			return &apiError{status: http.StatusBadGateway,
				msg: fmt.Sprintf("Copying dataset %q to the hermod-ml worker failed: %v", name, err)}
		}
	}
	if total != t.Rows {
		e.synced.set(key, "")
		return &apiError{status: http.StatusConflict, msg: fmt.Sprintf("The hermod-ml worker's copy of dataset %q has %d rows, "+
			"not the %d copied: something else wrote to it meanwhile. Try again.", name, total, t.Rows)}
	}
	e.synced.set(key, state)
	return nil
}
