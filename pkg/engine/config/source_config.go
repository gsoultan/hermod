package config

import "time"

// SourceConfig holds configuration for a specific source.
type SourceConfig struct {
	ReconnectIntervals []time.Duration `json:"reconnect_intervals"`
}
