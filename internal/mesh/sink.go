// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

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
