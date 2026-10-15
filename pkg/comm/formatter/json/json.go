// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
