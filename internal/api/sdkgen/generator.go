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
