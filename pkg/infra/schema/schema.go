// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
