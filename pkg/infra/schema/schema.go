// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package schema

import (
	"context"
	"fmt"
)

// SchemaType defines the supported schema formats.
type SchemaType string

const (
	Avro       SchemaType = "avro"
	JSONSchema SchemaType = "json"
	Protobuf   SchemaType = "protobuf"
)

// SchemaConfig contains the configuration for schema validation.
type SchemaConfig struct {
	Type   SchemaType `json:"type"`
	Schema string     `json:"schema"` // The raw schema definition or URL
}

// Validator defines the interface for schema validation.
type Validator interface {
	Validate(ctx context.Context, data map[string]any) error
	Type() SchemaType
}

// NewValidator creates a new validator based on the provided configuration.
func NewValidator(config SchemaConfig) (Validator, error) {
	switch config.Type {
	case Avro:
		return NewAvroValidator(config.Schema)
	case JSONSchema:
		return NewJSONSchemaValidator(config.Schema)
	case Protobuf:
		return NewProtobufValidator(config.Schema)
	default:
		return nil, fmt.Errorf("unsupported schema type: %s", config.Type)
	}
}
