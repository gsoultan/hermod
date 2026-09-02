package cdc

import (
	"context"

	hermod "github.com/gsoultan/Hermod"
)

// Connector defines the interface for Change Data Capture connectors.
type Connector interface {
	hermod.Source
	// Stream starts the CDC streaming from a specific checkpoint.
	Stream(ctx context.Context, checkpoint string) error
}
