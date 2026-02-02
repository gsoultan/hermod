package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/user/hermod"
)

// Router handles inter-cluster message routing for the Hermod Mesh.
type Router struct {
	mu       sync.RWMutex
	clusters map[string]*ClusterNode
	logger   hermod.Logger
}

type ClusterNode struct {
	ID       string
	Endpoint string
	Region   string
	Status   string // "online", "offline"
	Filters  []string
}

func NewRouter(logger hermod.Logger) *Router {
	return &Router{
		clusters: make(map[string]*ClusterNode),
		logger:   logger,
	}
}

func (r *Router) PushFilter(clusterID string, filter string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if node, ok := r.clusters[clusterID]; ok {
		node.Filters = append(node.Filters, filter)
		r.logger.Info("Mesh: filter pushed to edge cluster", "cluster_id", clusterID, "filter", filter)
	}
}

func (r *Router) RegisterCluster(id, endpoint, region string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clusters[id] = &ClusterNode{
		ID:       id,
		Endpoint: endpoint,
		Region:   region,
		Status:   "online",
	}
	r.logger.Info("Mesh: cluster registered", "cluster_id", id, "endpoint", endpoint, "region", region)
}

func (r *Router) RouteMessage(ctx context.Context, targetClusterID string, msg hermod.Message) error {
	// If targetClusterID is empty, try to resolve from message metadata (Geo-aware sharding)
	if targetClusterID == "" {
		targetClusterID = msg.Metadata()["target_cluster"]
	}

	// Dynamic Routing: If region metadata exists, try to find a cluster in that region
	region := msg.Metadata()["target_region"]

	r.mu.RLock()
	var node *ClusterNode
	if targetClusterID != "" {
		node = r.clusters[targetClusterID]
	} else if region != "" {
		// Region-aware routing logic
		for _, n := range r.clusters {
			if n.Status == "online" && strings.EqualFold(n.Region, region) {
				node = n
				break
			}
		}
	}
	r.mu.RUnlock()

	if node == nil {
		if targetClusterID != "" {
			return fmt.Errorf("mesh: target cluster %s not found", targetClusterID)
		}
		// Default to local processing if no target cluster or region resolved
		r.logger.Debug("Mesh: no remote target resolved, processing locally", "message_id", msg.ID())
		return nil
	}

	if node.Status != "online" {
		return fmt.Errorf("mesh: target cluster %s is offline", targetClusterID)
	}

	// Intelligent Edge Filtering: Check if message matches any push-down filters
	for _, f := range node.Filters {
		// Simple pattern matching for simulation
		if strings.Contains(strings.ToLower(msg.ID()), strings.ToLower(f)) {
			r.logger.Info("Mesh: message filtered at edge (push-down match)", "filter", f, "message_id", msg.ID())
			return nil // Filtered out, don't send
		}
	}

	// Real Forwarding Logic
	r.logger.Info("Mesh: routing message to peer cluster",
		"target_id", node.ID,
		"endpoint", node.Endpoint,
		"message_id", msg.ID())

	// Implement HTTP forwarding
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mesh: marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", node.Endpoint+"/api/mesh/receive", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("mesh: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hermod-Cluster-ID", node.ID)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mesh: forward request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mesh: target cluster returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
