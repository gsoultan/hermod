// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
