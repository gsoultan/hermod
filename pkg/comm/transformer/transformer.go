// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package transformer

import (
	"context"
	"maps"
	"slices"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/security/pii"
)

var piiEngine = pii.NewEngine()

func PIIEngine() *pii.Engine {
	return piiEngine
}

// Transformer defines the interface for data transformations.
type Transformer interface {
	Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error)
}

// PreparedTransformer allows pre-parsing configuration for better performance.
type PreparedTransformer interface {
	Transformer
	Prepare(config map[string]any) (map[string]any, error)
}

// Registry manages the available transformers.
type Registry struct {
	transformers map[string]Transformer
}

// NewRegistry creates a new Transformer Registry.
func NewRegistry() *Registry {
	return &Registry{
		transformers: make(map[string]Transformer),
	}
}

// Register adds a transformer to the registry.
func (r *Registry) Register(name string, t Transformer) {
	r.transformers[name] = t
}

// Get retrieves a transformer by name.
func (r *Registry) Get(name string) (Transformer, bool) {
	t, ok := r.transformers[name]
	return t, ok
}

var defaultRegistry = NewRegistry()

// Register adds a transformer to the default registry.
func Register(name string, t Transformer) {
	defaultRegistry.Register(name, t)
}

// Get retrieves a transformer from the default registry.
func Get(name string) (Transformer, bool) {
	return defaultRegistry.Get(name)
}

// Names lists the registered transformer names, sorted.
func (r *Registry) Names() []string {
	return slices.Sorted(maps.Keys(r.transformers))
}

// Names lists the transformers in the default registry, sorted. Registration
// happens in init functions, so the list is complete once the packages that
// register (blank-imported by the binary) have been initialised.
func Names() []string {
	return defaultRegistry.Names()
}
