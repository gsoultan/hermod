package genai

import (
	"strings"
	"testing"
)

const invoiceSchema = `{"type":"object","properties":{"invoice_no":{"type":"string"},"total":{"type":"number"}},"required":["invoice_no","total"]}`

func TestExtract_ValidatesAndMerges(t *testing.T) {
	f := newFakeLLM(t, `{"invoice_no":"INV-7","total":12.5}`)
	data, err := run(t, "ai_extract", f.config(map[string]any{"schema": invoiceSchema, "instructions": "Read the invoice."}), map[string]any{"text": "Invoice INV-7, total 12.50"})
	if err != nil {
		t.Fatal(err)
	}
	if data["invoice_no"] != "INV-7" || data["total"] != 12.5 {
		t.Fatalf("data = %v", data)
	}
	body := f.bodies[0]
	rf, _ := body["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Errorf("structured output not requested: %v", body["response_format"])
	}
	if !strings.Contains(f.lastUserText(), "INV-7") {
		t.Errorf("input not sent: %q", f.lastUserText())
	}
}

func TestExtract_RetriesOnceWithTheValidationError(t *testing.T) {
	f := newFakeLLM(t, `{"invoice_no":"INV-7"}`, `{"invoice_no":"INV-7","total":3}`)
	data, err := run(t, "ai_extract", f.config(map[string]any{"schema": invoiceSchema, "targetField": "invoice"}), map[string]any{"text": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if inv, _ := data["invoice"].(map[string]any); inv["total"] != float64(3) {
		t.Fatalf("data = %v", data)
	}
	if f.calls() != 2 || !strings.Contains(f.lastUserText(), "total") {
		t.Fatalf("calls = %d, retry prompt = %q", f.calls(), f.lastUserText())
	}
}

func TestExtract_FailsAfterSecondInvalidAnswer(t *testing.T) {
	f := newFakeLLM(t, `{"invoice_no":1}`)
	if _, err := run(t, "ai_extract", f.config(map[string]any{"schema": invoiceSchema}), map[string]any{"text": "x"}); err == nil {
		t.Fatal("expected an error after two invalid answers")
	}
	if f.calls() != 2 {
		t.Fatalf("calls = %d", f.calls())
	}
}

func TestExtract_RequiresAValidSchema(t *testing.T) {
	f := newFakeLLM(t, `{}`)
	for _, schema := range []string{"", "{not json"} {
		if _, err := run(t, "ai_extract", f.config(map[string]any{"schema": schema}), nil); err == nil {
			t.Errorf("schema %q accepted", schema)
		}
	}
	if f.calls() != 0 {
		t.Fatal("a bad schema must fail before calling the model")
	}
}

func TestEmbed_WritesVector(t *testing.T) {
	f := newFakeLLM(t, `{"data":[{"index":0,"embedding":[0.5,0.25]}],"usage":{"prompt_tokens":2}}`)
	data, err := run(t, "ai_embed", f.config(map[string]any{"inputField": "body"}), map[string]any{"body": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	vec, _ := data["embedding"].([]float32)
	if len(vec) != 2 || vec[0] != 0.5 {
		t.Fatalf("embedding = %#v", data["embedding"])
	}
	if in := f.bodies[0]["input"].([]any); in[0] != "hello" {
		t.Fatalf("input = %v", in)
	}
}

func TestEmbed_UnsupportedProviderFails(t *testing.T) {
	if _, err := run(t, "ai_embed", map[string]any{"provider": "anthropic", "apiKey": "k", "inputField": "b"}, map[string]any{"b": "x"}); err == nil {
		t.Fatal("anthropic has no embeddings endpoint; ai_embed must say so")
	}
}
