package cdc

import (
	"context"

	"github.com/user/hermod"
)

// Connector defines the interface for Change Data Capture connectors.
type Connector interface {
	hermod.Source
	// Stream starts the CDC streaming from a specific checkpoint.
	Stream(ctx context.Context, checkpoint string) error
}
