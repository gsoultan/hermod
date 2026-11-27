// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package scale

import (
	"context"
	"fmt"
	"sync"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// scaleStorage is the metadata store the harness's registries read from. It
// holds sources, sinks and workflows in memory and accepts every status and
// statistics write, so what is measured is the engine and the databases it
// talks to, not a metadata backend.
type scaleStorage struct {
	testutil.BaseMockStorage

	mu        sync.RWMutex
	sources   map[string]storage.Source
	sinks     map[string]storage.Sink
	workflows map[string]storage.Workflow
}

func newScaleStorage() *scaleStorage {
	return &scaleStorage{
		sources:   make(map[string]storage.Source),
		sinks:     make(map[string]storage.Sink),
		workflows: make(map[string]storage.Workflow),
	}
}

func (s *scaleStorage) putSource(src storage.Source) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[src.ID] = src
}

func (s *scaleStorage) putSink(snk storage.Sink) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sinks[snk.ID] = snk
}

func (s *scaleStorage) putWorkflow(wf storage.Workflow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workflows[wf.ID] = wf
}

func (s *scaleStorage) GetSource(_ context.Context, id string) (storage.Source, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src, ok := s.sources[id]
	if !ok {
		return storage.Source{}, fmt.Errorf("scale: source %q: %w", id, storage.ErrNotFound)
	}
	return src, nil
}

func (s *scaleStorage) GetSink(_ context.Context, id string) (storage.Sink, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snk, ok := s.sinks[id]
	if !ok {
		return storage.Sink{}, fmt.Errorf("scale: sink %q: %w", id, storage.ErrNotFound)
	}
	return snk, nil
}

func (s *scaleStorage) GetWorkflow(_ context.Context, id string) (storage.Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wf, ok := s.workflows[id]
	if !ok {
		return storage.Workflow{}, fmt.Errorf("scale: workflow %q: %w", id, storage.ErrNotFound)
	}
	return wf, nil
}
