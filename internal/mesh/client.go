// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gsoultan/hermod"
)

// MeshClient handles HTTP communication with remote clusters.
type MeshClient struct {
	endpoint string
}

// Forward sends a message to the remote cluster's receive endpoint.
func (c *MeshClient) Forward(ctx context.Context, msg hermod.Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mesh: marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/mesh/receive", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mesh: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mesh: forward request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mesh: target cluster returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
