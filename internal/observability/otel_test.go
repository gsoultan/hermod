// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
