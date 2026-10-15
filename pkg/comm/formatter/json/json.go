// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package json

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gsoultan/hermod"
)

type JSONMode string

const (
	ModeFull    JSONMode = "full"
	ModePayload JSONMode = "payload"
)

type JSONFormatter struct {
	Mode JSONMode
}

func NewJSONFormatter() *JSONFormatter {
	return &JSONFormatter{Mode: ModeFull}
}

func (f *JSONFormatter) SetMode(mode JSONMode) {
	f.Mode = mode
}

func (f *JSONFormatter) Format(msg hermod.Message) ([]byte, error) {
	if msg == nil {
		return nil, errors.New("message is nil")
	}
	if f.Mode == ModePayload {
		payload := msg.Payload()
		if len(payload) == 0 {
			return []byte("{}"), nil
		}
		return payload, nil
	}

	// Default formatting uses the message's standard JSON representation
	if marshaler, ok := msg.(json.Marshaler); ok {
		return marshaler.MarshalJSON()
	}

	// Fallback for other message implementations
	data, err := json.Marshal(map[string]any{
		"id":        msg.ID(),
		"operation": msg.Operation(),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"data":      msg.Data(),
		"metadata":  msg.Metadata(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message to JSON: %w", err)
	}
	return data, nil
}
