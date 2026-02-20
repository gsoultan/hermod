package transformer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestAITransformer_Transform(t *testing.T) {
	// Mock OpenAI API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]string{
							"content": "Positive",
						},
					},
				},
			})
		}
	}))
	defer server.Close()

	tf := &AITransformer{}
	msg := message.AcquireMessage()
	msg.SetData("text", "I love this product!")

	config := map[string]any{
		"provider":    "openai",
		"endpoint":    server.URL + "/v1/chat/completions",
		"model":       "gpt-3.5-turbo",
		"prompt":      "Analyze sentiment:",
		"targetField": "sentiment",
	}

	result, err := tf.Transform(context.Background(), msg, config)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	if result.Data()["sentiment"] != "Positive" {
		t.Errorf("Expected sentiment 'Positive', got '%v'", result.Data()["sentiment"])
	}
}

func TestAITransformer_Transform_JSONMerge(t *testing.T) {
	// Mock Ollama API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"response": `{"category": "electronics", "priority": "high"}`,
		})
	}))
	defer server.Close()

	tf := &AITransformer{}
	msg := message.AcquireMessage()
	msg.SetData("item", "laptop")

	config := map[string]any{
		"provider": "ollama",
		"endpoint": server.URL,
		"model":    "llama2",
		"prompt":   "Classify item into JSON:",
	}

	result, err := tf.Transform(context.Background(), msg, config)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	if result.Data()["category"] != "electronics" {
		t.Errorf("Expected category 'electronics', got '%v'", result.Data()["category"])
	}
	if result.Data()["priority"] != "high" {
		t.Errorf("Expected priority 'high', got '%v'", result.Data()["priority"])
	}
}

func TestAIMapperTransformer_Transform(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"response": `{"full_name": "John Doe", "age": 30}`,
		})
	}))
	defer server.Close()

	tf := &AIMapperTransformer{}
	msg := message.AcquireMessage()
	msg.SetData("name", "John")
	msg.SetData("surname", "Doe")
	msg.SetData("birth_year", 1994)

	config := map[string]any{
		"provider":     "ollama",
		"endpoint":     server.URL,
		"model":        "llama2",
		"targetSchema": `{"type": "object", "properties": {"full_name": {"type": "string"}, "age": {"type": "integer"}}}`,
	}

	result, err := tf.Transform(context.Background(), msg, config)
	if err != nil {
		t.Fatalf("Transform failed: %v", err)
	}

	if result.Data()["full_name"] != "John Doe" {
		t.Errorf("Expected full_name 'John Doe', got '%v'", result.Data()["full_name"])
	}
	if result.Data()["age"].(float64) != 30 && result.Data()["age"] != 30 {
		t.Errorf("Expected age 30, got '%v'", result.Data()["age"])
	}
}
