// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
