package transformer

import (
	"context"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/pii"
)

var piiEngine = pii.NewEngine()

func PIIEngine() *pii.Engine {
	return piiEngine
}

// Transformer defines the interface for data transformations.
type Transformer interface {
	Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error)
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
