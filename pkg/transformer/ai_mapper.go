package transformer

import (
	"context"
	"fmt"

	"github.com/user/hermod"
)

func init() {
	Register("ai_mapper", &AIMapper{})
}

// AIMapper uses LLMs to map unstructured data to a structured target schema.
type AIMapper struct {
	ai AITransformer
}

func (t *AIMapper) Transform(ctx context.Context, msg hermod.Message, config map[string]any) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}

	targetSchema, _ := config["targetSchema"].(string) // JSON representation of the target schema
	hints, _ := config["hints"].(string)               // Optional hints for the AI

	// Prepare a specialized prompt for mapping
	prompt := fmt.Sprintf(`Map the following unstructured data to the target schema. 
Return ONLY a JSON object that strictly follows the schema.
Target Schema: %s
%s`, targetSchema, hints)

	// Reuse AITransformer logic
	mapperConfig := make(map[string]any)
	for k, v := range config {
		mapperConfig[k] = v
	}
	mapperConfig["prompt"] = prompt
	mapperConfig["targetField"] = "" // We want to merge the JSON result

	return t.ai.Transform(ctx, msg, mapperConfig)
}
