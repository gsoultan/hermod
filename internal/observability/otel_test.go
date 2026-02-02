package observability

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/config"
)

func TestInitOTLP_Basic(t *testing.T) {
	cfg := config.OTLPConfig{
		Endpoint:    "localhost:4317",
		Protocol:    "grpc",
		ServiceName: "hermod-test",
		Insecure:    true,
	}

	shutdown, err := InitOTLP(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Failed to init OTLP: %v", err)
	}

	if shutdown == nil {
		t.Fatal("Shutdown function is nil")
	}

	// Clean up
	_ = shutdown(context.Background())
}

func TestInitOTLP_HTTP(t *testing.T) {
	cfg := config.OTLPConfig{
		Endpoint:    "localhost:4318",
		Protocol:    "http",
		ServiceName: "hermod-test",
		Insecure:    true,
	}

	shutdown, err := InitOTLP(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Failed to init OTLP HTTP: %v", err)
	}

	if shutdown == nil {
		t.Fatal("Shutdown function is nil")
	}

	// Clean up
	_ = shutdown(context.Background())
}
