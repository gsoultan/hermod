// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package observability

import (
	"testing"

	"github.com/gsoultan/hermod/internal/config"
)

func TestInitOTLP_Basic(t *testing.T) {
	cfg := config.OTLPConfig{
		Endpoint:    "localhost:4317",
		Protocol:    "grpc",
		ServiceName: "hermod-test",
		Insecure:    true,
	}

	shutdown, err := InitOTLP(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Failed to init OTLP: %v", err)
	}

	if shutdown == nil {
		t.Fatal("Shutdown function is nil")
	}

	// Clean up
	_ = shutdown(t.Context())
}

func TestInitOTLP_HTTP(t *testing.T) {
	cfg := config.OTLPConfig{
		Endpoint:    "localhost:4318",
		Protocol:    "http",
		ServiceName: "hermod-test",
		Insecure:    true,
	}

	shutdown, err := InitOTLP(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Failed to init OTLP HTTP: %v", err)
	}

	if shutdown == nil {
		t.Fatal("Shutdown function is nil")
	}

	// Clean up
	_ = shutdown(t.Context())
}
