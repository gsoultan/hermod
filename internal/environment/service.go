// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package environment

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// Store is what the service reads and writes besides its settings.
type Store interface {
	Settings
	GetWorkflow(ctx context.Context, id string) (storage.Workflow, error)
	GetWorkflowVersion(ctx context.Context, workflowID string, version int) (storage.WorkflowVersion, error)
	ListWorkflowVersions(ctx context.Context, workflowID string) ([]storage.WorkflowVersion, error)
	ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error)
	ListSinks(ctx context.Context, filter storage.CommonFilter) ([]storage.Sink, int, error)
	GetSource(ctx context.Context, id string) (storage.Source, error)
	GetSink(ctx context.Context, id string) (storage.Sink, error)
	ListWorkflows(ctx context.Context, filter storage.CommonFilter) ([]storage.Workflow, int, error)
	ListVHosts(ctx context.Context, filter storage.CommonFilter) ([]storage.VHost, int, error)
	CreateVHost(ctx context.Context, vhost storage.VHost) error
}

// Applier is the workflow update path: the same validation, stored row and
// version history an edit in the console goes through.
type Applier interface {
	// Validate returns the first error-level problem with wf, or nil.
	Validate(ctx context.Context, wf storage.Workflow) error
	// Portable returns wf without this instance's runtime columns and the
	// credentials a bundle never carries.
	Portable(wf storage.Workflow) storage.Workflow
	ApplyWorkflowChange(ctx context.Context, before, after storage.Workflow, username, message string) (previous, applied int, err error)
	// CreateWorkflowVersioned stores a new workflow as version 1.
	CreateWorkflowVersioned(ctx context.Context, wf storage.Workflow, username, message string) (applied int, err error)
	// RollbackWorkflowTo restores version and records the restore as a new
	// version, which it returns.
	RollbackWorkflowTo(ctx context.Context, id string, version int, username string) (applied int, err error)
}

// SecretChecker answers whether a secret or env reference resolves on this
// instance for a vhost.
type SecretChecker interface {
	HasSecret(ctx context.Context, vhost, name string) bool
	HasEnv(name string) bool
}

// Service links environments and moves promotions between them.
type Service struct {
	store   Store
	applier Applier
	remote  Remote
	secrets SecretChecker
	version string

	// mu serialises read-modify-write of the settings within this process.
	mu sync.Mutex
	// pairFailures counts wrong pairing codes since the last success or
	// reset; see Pair.
	pairFailures int
	now          func() time.Time
}

// NewService builds the service. remote is how this instance calls the ones
// it links to; version is this build's version, reported to them.
func NewService(store Store, applier Applier, remote Remote, secrets SecretChecker, version string) *Service {
	return &Service{store: store, applier: applier, remote: remote, secrets: secrets, version: version, now: time.Now}
}

// Instance returns how this instance is described.
func (s *Service) Instance(ctx context.Context) (Instance, error) {
	return loadInstance(ctx, s.store)
}

// SetInstance describes this instance.
func (s *Service) SetInstance(ctx context.Context, in Instance) (Instance, error) {
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) > 64 {
		return Instance{}, fmt.Errorf("%w: the name must be at most 64 characters", ErrInvalid)
	}
	if !in.Role.Valid() {
		return Instance{}, fmt.Errorf("%w: the role must be development, staging or production", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return in, putJSON(ctx, s.store, keyInstance, in)
}

// Rules returns what this instance requires of promotions sent to it.
func (s *Service) Rules(ctx context.Context) (Rules, error) {
	return loadRules(ctx, s.store)
}

// SetRules changes what this instance requires of promotions sent to it.
func (s *Service) SetRules(ctx context.Context, r Rules) (Rules, error) {
	if err := r.Validate(); err != nil {
		return Rules{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return r, putJSON(ctx, s.store, keyRules, r)
}

// displayName is the name an instance shows to others: its own, or its role.
func displayName(in Instance) string {
	if in.Name != "" {
		return in.Name
	}
	if in.Role != "" {
		return strings.ToUpper(string(in.Role[:1])) + string(in.Role[1:])
	}
	return "Hermod"
}
