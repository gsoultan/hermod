package transformer

import (
	"context"
	"testing"

	"github.com/user/hermod/pkg/message"
)

func TestMaskTransformer_PII(t *testing.T) {
	tr := &MaskTransformer{}
	ctx := context.Background()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Credit Card",
			input:    "My card is 4111111111111111",
			expected: "My card is ****-****-****-****",
		},
		{
			name:     "SSN",
			input:    "SSN: 123-45-6789",
			expected: "SSN: ***-**-****",
		},
		{
			name:     "IPv4",
			input:    "IP: 192.168.1.1",
			expected: "IP: *.*.*.*",
		},
		{
			name:     "IPv6",
			input:    "IP: 2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			expected: "IP: ****:****:****:****:****:****:****:****",
		},
		{
			name:     "Email",
			input:    "Contact me at john.doe@example.com",
			expected: "Contact me at ****@****.***",
		},
		{
			name:     "Phone",
			input:    "Call 1-800-555-0199",
			expected: "Call (***) ***-****",
		},
		{
			name:     "IBAN",
			input:    "Bank: DE12345678901234567890",
			expected: "Bank: **** **** **** ****",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := message.AcquireMessage()
			defer message.ReleaseMessage(msg)
			msg.SetID("1")
			msg.SetData("content", tt.input)

			config := map[string]any{
				"field":    "content",
				"maskType": "pii",
			}

			res, err := tr.Transform(ctx, msg, config)
			if err != nil {
				t.Fatalf("Transform failed: %v", err)
			}

			got := res.Data()["content"].(string)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}
