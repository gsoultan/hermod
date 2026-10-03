package factory

import (
	"time"

	"github.com/gsoultan/hermod"
)

type SourceConfig struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// VHost is the vhost the source belongs to: a `secret:NAME` in its config
	// is resolved from that vhost's secrets first.
	VHost              string           `json:"vhost,omitempty"`
	Config             hermod.StringMap `json:"config"`
	State              hermod.StringMap `json:"state"`
	ReconnectIntervals []time.Duration  `json:"-"`
}

type SinkConfig struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// VHost is the vhost the sink belongs to; see SourceConfig.VHost.
	VHost  string           `json:"vhost,omitempty"`
	Config hermod.StringMap `json:"config"`
}
