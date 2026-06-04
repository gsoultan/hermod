package mesh

import (
	"context"
	"sync"

	"github.com/user/hermod"
)

// Manager handles inter-cluster routing and mesh topology.
type Manager struct {
	mu       sync.RWMutex
	clusters map[string]Cluster
	logger   hermod.Logger
}

// NewManager creates a new mesh Manager.
func NewManager(logger hermod.Logger) *Manager {
	return &Manager{
		clusters: make(map[string]Cluster),
		logger:   logger,
	}
}

// RegisterCluster adds a new cluster to the mesh.
func (m *Manager) RegisterCluster(_ context.Context, c Cluster) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusters[c.ID] = c
	m.logger.Info("Mesh: Cluster registered", "id", c.ID, "region", c.Region)
	return nil
}

// GetCluster returns a cluster by its ID.
func (m *Manager) GetCluster(id string) (Cluster, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clusters[id]
	return c, ok
}

// GetClusters returns all registered clusters.
func (m *Manager) GetClusters() []Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clusters := make([]Cluster, 0, len(m.clusters))
	for _, c := range m.clusters {
		clusters = append(clusters, c)
	}
	return clusters
}

// Failover marks a cluster as failed and redirects traffic if possible.
func (m *Manager) Failover(ctx context.Context, from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clusters[from]
	if !ok {
		return nil
	}
	c.Status = "Failed"
	m.clusters[from] = c
	m.logger.Warn("Mesh: Cluster marked as FAILED, failover initiated", "from", from, "to", to)
	return nil
}
