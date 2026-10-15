// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
