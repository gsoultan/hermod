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
