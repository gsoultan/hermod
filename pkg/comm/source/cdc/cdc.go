// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package cdc

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod"
)

// CDCPayload represents the structure of a change data capture event.
type CDCPayload struct {
	Before map[string]any `json:"before"`
	After  map[string]any `json:"after"`
	Op     string         `json:"op"` // c, u, d, r
	Source map[string]any `json:"source"`
	TS     int64          `json:"ts_ms"`
}

// Source represents a CDC source (e.g., Oracle LogMiner, IBM DB2).
type Source struct {
	Type   string
	Config map[string]any
}

func NewSource(sourceType string, config map[string]any) *Source {
	return &Source{
		Type:   sourceType,
		Config: config,
	}
}

func (s *Source) Read(ctx context.Context) (hermod.Message, error) {
	// Implementation would connect to specific database WAL/logs
	return nil, fmt.Errorf("cdc source %s not fully implemented", s.Type)
}

func (s *Source) Ack(ctx context.Context, msg hermod.Message) error {
	return nil
}

func (s *Source) Ping(ctx context.Context) error {
	return nil
}

func (s *Source) Close() error {
	return nil
}
