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

import "context"

// Cluster represents a remote Hermod cluster in the mesh.
type Cluster struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Status   string `json:"status"`
}

// MeshManager defines the interface for managing clusters in a mesh.
type MeshManager interface {
	RegisterCluster(ctx context.Context, c Cluster) error
	GetCluster(id string) (Cluster, bool)
	GetClusters() []Cluster
	Failover(ctx context.Context, fromClusterID, toClusterID string) error
}
