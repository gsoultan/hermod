package engine

import (
	"testing"
)

func TestBuildConnectionString_RabbitMQ_SSL(t *testing.T) {
	tests := []struct {
		name       string
		cfg        map[string]string
		sourceType string
		expected   string
	}{
		{
			name: "RabbitMQ Queue No SSL Default Port",
			cfg: map[string]string{
				"host": "localhost",
			},
			sourceType: "rabbitmq_queue",
			expected:   "amqp://localhost:5672/",
		},
		{
			name: "RabbitMQ Queue SSL Default Port",
			cfg: map[string]string{
				"host":    "localhost",
				"use_ssl": "true",
			},
			sourceType: "rabbitmq_queue",
			expected:   "amqps://localhost:5671/",
		},
		{
			name: "RabbitMQ Stream No SSL Default Port",
			cfg: map[string]string{
				"host": "localhost",
			},
			sourceType: "rabbitmq",
			expected:   "rabbitmq-stream://localhost:5552/",
		},
		{
			name: "RabbitMQ Stream SSL Default Port",
			cfg: map[string]string{
				"host":    "localhost",
				"use_ssl": "true",
			},
			sourceType: "rabbitmq",
			expected:   "rabbitmq-streams://localhost:5551/",
		},
		{
			name: "RabbitMQ Queue SSL Custom Port",
			cfg: map[string]string{
				"host":    "localhost",
				"port":    "1234",
				"use_ssl": "true",
			},
			sourceType: "rabbitmq_queue",
			expected:   "amqps://localhost:1234/",
		},
		{
			name: "RabbitMQ Queue with Credentials and VHost",
			cfg: map[string]string{
				"host":     "localhost",
				"user":     "user",
				"password": "pass",
				"dbname":   "vhost",
				"use_ssl":  "true",
			},
			sourceType: "rabbitmq_queue",
			expected:   "amqps://user:pass@localhost:5671/vhost",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildConnectionString(tt.cfg, tt.sourceType)
			if got != tt.expected {
				t.Errorf("BuildConnectionString() = %v, want %v", got, tt.expected)
			}
		})
	}
}
