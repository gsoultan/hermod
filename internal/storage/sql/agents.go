// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func (s *sqlStorage) CreateAgent(ctx context.Context, a storage.Agent) error {
	if err := storage.ValidateAgent(a); err != nil {
		return err
	}
	devices, status, err := encodeAgent(a.Devices, a.Status)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err := s.exec(ctx, s.queries.get(QueryInsertAgent), a.ID, a.VHost, a.Name, a.TokenHash,
		devices, status, a.CreatedBy, now, now); err != nil {
		return fmt.Errorf("saving agent %q: %w", a.Name, err)
	}
	return nil
}

func encodeAgent(devices []storage.AgentDevice, status storage.AgentStatus) (string, string, error) {
	d, err := json.Marshal(devices)
	if err != nil {
		return "", "", fmt.Errorf("encoding the agent's devices: %w", err)
	}
	st, err := json.Marshal(status)
	if err != nil {
		return "", "", fmt.Errorf("encoding the agent's status: %w", err)
	}
	return string(d), string(st), nil
}

// agentRow is the columns an agent query reads, in order.
type agentRow struct {
	id, vhost, name, hash, devices, status, by sql.NullString
	created, updated                           sql.NullTime
}

func (r *agentRow) targets() []any {
	return []any{&r.id, &r.vhost, &r.name, &r.hash, &r.devices, &r.status, &r.by, &r.created, &r.updated}
}

func (r *agentRow) agent() (storage.Agent, error) {
	a := storage.Agent{
		ID: r.id.String, VHost: r.vhost.String, Name: r.name.String, TokenHash: r.hash.String,
		CreatedBy: r.by.String, CreatedAt: r.created.Time, UpdatedAt: r.updated.Time,
	}
	if r.devices.String != "" {
		if err := json.Unmarshal([]byte(r.devices.String), &a.Devices); err != nil {
			return storage.Agent{}, fmt.Errorf("the devices of agent %q are unreadable: %w", a.ID, err)
		}
	}
	if r.status.String != "" {
		if err := json.Unmarshal([]byte(r.status.String), &a.Status); err != nil {
			return storage.Agent{}, fmt.Errorf("the status of agent %q is unreadable: %w", a.ID, err)
		}
	}
	return a, nil
}

func (s *sqlStorage) GetAgent(ctx context.Context, id string) (storage.Agent, error) {
	var r agentRow
	err := s.queryRow(ctx, s.queries.get(QueryGetAgent), id).Scan(r.targets()...)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.Agent{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.Agent{}, fmt.Errorf("reading agent %q: %w", id, err)
	}
	return r.agent()
}

func (s *sqlStorage) ListAgents(ctx context.Context, vhost string) ([]storage.Agent, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if vhost == "" {
		rows, err = s.query(ctx, s.queries.get(QueryListAllAgents))
	} else {
		rows, err = s.query(ctx, s.queries.get(QueryListAgents), vhost)
	}
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.Agent
	for rows.Next() {
		var r agentRow
		if err := rows.Scan(r.targets()...); err != nil {
			return nil, fmt.Errorf("listing agents: %w", err)
		}
		a, err := r.agent()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	return out, nil
}

func (s *sqlStorage) UpdateAgentDevices(ctx context.Context, id, name string, devices []storage.AgentDevice) error {
	current, err := s.GetAgent(ctx, id)
	if err != nil {
		return err
	}
	current.Name, current.Devices = name, devices
	if err := storage.ValidateAgent(current); err != nil {
		return err
	}
	raw, err := json.Marshal(devices)
	if err != nil {
		return fmt.Errorf("encoding the devices of agent %q: %w", id, err)
	}
	return s.updateAgent(ctx, id, "devices", QueryUpdateAgentDevices, name, string(raw), time.Now().UTC(), id)
}

func (s *sqlStorage) UpdateAgentStatus(ctx context.Context, id string, status storage.AgentStatus) error {
	raw, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("encoding the status of agent %q: %w", id, err)
	}
	return s.updateAgent(ctx, id, "status", QueryUpdateAgentStatus, string(raw), id)
}

func (s *sqlStorage) UpdateAgentToken(ctx context.Context, id, tokenHash string) error {
	return s.updateAgent(ctx, id, "token", QueryUpdateAgentToken, tokenHash, time.Now().UTC(), id)
}

func (s *sqlStorage) DeleteAgent(ctx context.Context, id string) error {
	return s.updateAgent(ctx, id, "row", QueryDeleteAgent, id)
}

// updateAgent runs a statement on one agent's row and reports ErrNotFound when
// there was no row to change.
func (s *sqlStorage) updateAgent(ctx context.Context, id, what, key string, args ...any) error {
	res, err := s.exec(ctx, s.queries.get(key), args...)
	if err != nil {
		return fmt.Errorf("saving the %s of agent %q: %w", what, id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("saving the %s of agent %q: %w", what, id, err)
	}
	if n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) DeleteAgents(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteAgentsOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the agents of vhost %q: %w", vhost, err)
	}
	return nil
}
