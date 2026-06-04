package mesh

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/user/hermod"
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

// Close closes any resources used by the MeshSink.
func (s *MeshSink) Close() error {
	return nil
}
