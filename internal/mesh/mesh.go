package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/user/hermod"
)

// Cluster represents a remote Hermod cluster in the mesh.
type Cluster struct {
	ID       string
	Endpoint string
	Region   string
	Status   string
}

// Manager handles inter-cluster routing and mesh topology.
type Manager struct {
	mu       sync.RWMutex
	clusters map[string]Cluster
	logger   hermod.Logger
}

func NewManager(logger hermod.Logger) *Manager {
	return &Manager{
		clusters: make(map[string]Cluster),
		logger:   logger,
	}
}

func (m *Manager) RegisterCluster(c Cluster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clusters[c.ID] = c
	m.logger.Info("Mesh: Cluster registered", "id", c.ID, "region", c.Region)
}

func (m *Manager) GetCluster(id string) (Cluster, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clusters[id]
	return c, ok
}

func (m *Manager) GetClusters() []Cluster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	clusters := make([]Cluster, 0, len(m.clusters))
	for _, c := range m.clusters {
		clusters = append(clusters, c)
	}
	return clusters
}

// MeshSink implements hermod.Sink to forward messages to other clusters.
type MeshSink struct {
	targetCluster Cluster
	client        *MeshClient
}

func NewMeshSink(target Cluster) *MeshSink {
	return &MeshSink{
		targetCluster: target,
		client:        &MeshClient{endpoint: target.Endpoint},
	}
}

func (s *MeshSink) Write(ctx context.Context, msg hermod.Message) error {
	return s.client.Forward(ctx, msg)
}

func (s *MeshSink) Ping(ctx context.Context) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", s.targetCluster.Endpoint+"/health", nil)
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

func (s *MeshSink) Close() error {
	return nil
}

type MeshClient struct {
	endpoint string
}

func (c *MeshClient) Forward(ctx context.Context, msg hermod.Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mesh: marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/api/mesh/receive", bytes.NewReader(payload))
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
