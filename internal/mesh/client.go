package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/hermod"
)

// MeshClient handles HTTP communication with remote clusters.
type MeshClient struct {
	endpoint string
}

// Forward sends a message to the remote cluster's receive endpoint.
func (c *MeshClient) Forward(ctx context.Context, msg hermod.Message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mesh: marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/mesh/receive", bytes.NewReader(payload))
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
