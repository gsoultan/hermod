package sdkgen

import "context"

// Generator defines the interface for generating client SDKs.
type Generator interface {
	// Generate produces the source code for a specific language.
	Generate(ctx context.Context, language string, schema any) ([]byte, error)
}
