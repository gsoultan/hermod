// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sdkgen

import (
	"context"
	"fmt"
)

// DefaultGenerator implements Generator for multiple languages.
type DefaultGenerator struct{}

// NewGenerator creates a new DefaultGenerator.
func NewGenerator() *DefaultGenerator {
	return &DefaultGenerator{}
}

// Generate produces a stub SDK for the requested language.
func (g *DefaultGenerator) Generate(_ context.Context, language string, _ any) ([]byte, error) {
	switch language {
	case "go":
		return []byte("// Hermod SDK for Go\npackage hermod\n\ntype Client struct {}\n"), nil
	case "python":
		return []byte("# Hermod SDK for Python\nclass HermodClient:\n    pass\n"), nil
	case "java":
		return []byte("// Hermod SDK for Java\npublic class HermodClient {}\n"), nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", language)
	}
}
