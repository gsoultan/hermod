package json

import (
	"encoding/json"
	"fmt"

	"github.com/user/hermod"
)

type JSONFormatter struct{}

func NewJSONFormatter() *JSONFormatter {
	return &JSONFormatter{}
}

func (f *JSONFormatter) Format(msg hermod.Message) ([]byte, error) {
	data, err := json.Marshal(map[string]interface{}{
		"id":        msg.ID(),
		"operation": msg.Operation(),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"before":    json.RawMessage(msg.Before()),
		"after":     json.RawMessage(msg.After()),
		"metadata":  msg.Metadata(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message to JSON: %w", err)
	}
	return data, nil
}
