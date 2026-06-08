package factory

import (
	"time"

	"github.com/user/hermod"
)

type SourceConfig struct {
	ID                 string           `json:"id"`
	Type               string           `json:"type"`
	Config             hermod.StringMap `json:"config"`
	State              hermod.StringMap `json:"state"`
	ReconnectIntervals []time.Duration  `json:"-"`
}

type SinkConfig struct {
	ID     string           `json:"id"`
	Type   string           `json:"type"`
	Config hermod.StringMap `json:"config"`
}
