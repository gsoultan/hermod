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
