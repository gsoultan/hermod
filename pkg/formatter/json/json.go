package json

import (
	"encoding/json"
	"fmt"

	"github.com/user/hermod"
)

type JSONMode string

const (
	ModeFull    JSONMode = "full"
	ModePayload JSONMode = "payload"
)

type JSONFormatter struct {
	Mode JSONMode
}

func NewJSONFormatter() *JSONFormatter {
	return &JSONFormatter{Mode: ModeFull}
}

func (f *JSONFormatter) SetMode(mode JSONMode) {
	f.Mode = mode
}

func (f *JSONFormatter) Format(msg hermod.Message) ([]byte, error) {
	if f.Mode == ModePayload {
		payload := msg.Payload()
		if len(payload) == 0 {
			return []byte("{}"), nil
		}
		return payload, nil
	}

	// Default formatting uses the message's standard JSON representation
	if marshaler, ok := msg.(json.Marshaler); ok {
		return marshaler.MarshalJSON()
	}

	// Fallback for other message implementations
	data, err := json.Marshal(map[string]interface{}{
		"id":        msg.ID(),
		"operation": msg.Operation(),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"data":      msg.Data(),
		"metadata":  msg.Metadata(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message to JSON: %w", err)
	}
	return data, nil
}
