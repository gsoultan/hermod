package storage

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestValidateAIBudget(t *testing.T) {
	priced := []AIModelPrice{{Model: AnyModel, InputPerMillion: 1, OutputPerMillion: 2}}
	cases := []struct {
		name    string
		b       AIBudget
		wantErr string
	}{
		{"kill switch only", AIBudget{VHost: "v", Disabled: true}, ""},
		{"token limits", AIBudget{VHost: "v", MonthlyTokens: 10, Workflows: []AIWorkflowCap{{WorkflowID: "wf", MonthlyTokens: 5}}}, ""},
		{"cost with default price", AIBudget{VHost: "v", MonthlyCost: 10, Prices: priced}, ""},
		{"no vhost", AIBudget{}, "one vhost"},
		{"all is not a vhost", AIBudget{VHost: "all"}, "one vhost"},
		{"negative tokens", AIBudget{VHost: "v", MonthlyTokens: -1}, "negative"},
		{"NaN cost", AIBudget{VHost: "v", MonthlyCost: math.NaN()}, "negative"},
		{"vhost cost without default price", AIBudget{VHost: "v", MonthlyCost: 1, Prices: []AIModelPrice{{Model: "m"}}}, `"*" price`},
		{"workflow cost without default price", AIBudget{VHost: "v", Workflows: []AIWorkflowCap{{WorkflowID: "wf", MonthlyCost: 1}}}, `"*" price`},
		{"duplicate price", AIBudget{VHost: "v", Prices: []AIModelPrice{{Model: "m"}, {Model: "m"}}}, "twice"},
		{"negative price", AIBudget{VHost: "v", Prices: []AIModelPrice{{Model: "m", InputPerMillion: -1}}}, "negative"},
		{"price without model", AIBudget{VHost: "v", Prices: []AIModelPrice{{}}}, "model id"},
		{"cap without workflow", AIBudget{VHost: "v", Workflows: []AIWorkflowCap{{MonthlyTokens: 1}}}, "workflow_id"},
		{"duplicate cap", AIBudget{VHost: "v", Workflows: []AIWorkflowCap{{WorkflowID: "a"}, {WorkflowID: "a"}}}, "twice"},
		{"long currency", AIBudget{VHost: "v", Currency: strings.Repeat("x", 9)}, "currency"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAIBudget(tc.b)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestAIBudgetCostMicros(t *testing.T) {
	b := AIBudget{Prices: []AIModelPrice{
		{Model: "big", InputPerMillion: 15, OutputPerMillion: 75},
		{Model: AnyModel, InputPerMillion: 1, OutputPerMillion: 2},
	}}
	// 1000 in at 15/M plus 200 out at 75/M is 0.015 + 0.015 = 0.03.
	if got := b.CostMicros("big", 1000, 200); got != 30_000 {
		t.Errorf("big = %d micros, want 30000", got)
	}
	// An unnamed model takes the "*" price: 0.001 + 0.0004.
	if got := b.CostMicros("other", 1000, 200); got != 1_400 {
		t.Errorf("other = %d micros, want 1400", got)
	}
	if got := (AIBudget{}).CostMicros("x", 1000, 1000); got != 0 {
		t.Errorf("an unpriced model cost %d", got)
	}
	if CostToMicros(2.5) != 2_500_000 {
		t.Errorf("CostToMicros(2.5) = %d", CostToMicros(2.5))
	}
}

func TestAIBudgetLimited(t *testing.T) {
	if (AIBudget{VHost: "v", Prices: []AIModelPrice{{Model: AnyModel, InputPerMillion: 1}}}).Limited() {
		t.Error("a price list alone limits nothing")
	}
	for name, b := range map[string]AIBudget{
		"kill switch":   {Disabled: true},
		"vhost tokens":  {MonthlyTokens: 1},
		"workflow cost": {Workflows: []AIWorkflowCap{{WorkflowID: "w", MonthlyCost: 1}}},
	} {
		if !b.Limited() {
			t.Errorf("%s does not count as a limit", name)
		}
	}
}

func TestAIUsagePeriodIsTheUTCMonth(t *testing.T) {
	// 00:30 on 1 March in UTC+7 is still February in UTC.
	at := time.Date(2026, 3, 1, 0, 30, 0, 0, time.FixedZone("ICT", 7*3600))
	if got := AIUsagePeriod(at); got != "2026-02" {
		t.Fatalf("period = %q, want 2026-02", got)
	}
}

func TestAIUsageIDIsUnambiguous(t *testing.T) {
	if AIUsageID("a/b", "2026-01", "c") == AIUsageID("a", "2026-01", "b/c") {
		t.Fatal("two different scopes share a usage key")
	}
	if AIUsageID("v", "2026-01", "") == AIUsageID("v", "2026-02", "") {
		t.Fatal("two months share a usage key")
	}
}
