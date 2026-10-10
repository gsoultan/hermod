package selfheal_test

import (
	"testing"

	"github.com/gsoultan/hermod/internal/selfheal"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestAdvisorIsOffUnlessConfigured(t *testing.T) {
	adv, err := selfheal.AdvisorFromEnv(env(nil), newStore())
	if err != nil || adv != nil {
		t.Fatalf("advisor %v, err %v", adv, err)
	}
}

func TestAdvisorRefusesAPlaintextKey(t *testing.T) {
	adv, err := selfheal.AdvisorFromEnv(env(map[string]string{
		selfheal.EnvProvider: "openai",
		selfheal.EnvAPIKey:   "sk-plain",
	}), newStore())
	if err == nil || adv != nil {
		t.Fatalf("advisor %v, err %v", adv, err)
	}
}

func TestAdvisorAcceptsASecretReference(t *testing.T) {
	adv, err := selfheal.AdvisorFromEnv(env(map[string]string{
		selfheal.EnvProvider: "openai",
		selfheal.EnvAPIKey:   `{{secret("OPENAI_API_KEY")}}`,
	}), newStore())
	if err != nil || adv == nil {
		t.Fatalf("advisor %v, err %v", adv, err)
	}
}
