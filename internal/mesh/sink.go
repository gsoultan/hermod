// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package mesh

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gsoultan/hermod"
)

// MeshSink implements hermod.Sink to forward messages to other clusters.
type MeshSink struct {
	targetCluster Cluster
	client        *MeshClient
}

// NewMeshSink creates a new MeshSink.
func NewMeshSink(target Cluster) *MeshSink {
	return &MeshSink{
		targetCluster: target,
		client:        &MeshClient{endpoint: target.Endpoint},
	}
}

// Write forwards the message to the target cluster.
func (s *MeshSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.client.Forward(ctx, msg)
}

// Ping checks the health of the target cluster.
func (s *MeshSink) Ping(ctx context.Context) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.targetCluster.Endpoint+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mesh cluster health check failed: %d", resp.StatusCode)
	}
	return nil
}

// Close closes any resources used by the MeshSink.
func (s *MeshSink) Close() error {
	return nil
}
