// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
