package factory

import "time"

type SourceConfig struct {
	ID                 string            `json:"id"`
	Type               string            `json:"type"`
	Config             map[string]string `json:"config"`
	State              map[string]string `json:"state"`
	ReconnectIntervals []time.Duration   `json:"-"`
}

type SinkConfig struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}
