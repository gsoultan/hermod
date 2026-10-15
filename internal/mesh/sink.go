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
