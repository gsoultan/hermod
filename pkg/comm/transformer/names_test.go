package transformer

import (
	"context"
	"slices"
	"testing"

	"github.com/gsoultan/hermod"
)

type nopTransformer struct{}

func (nopTransformer) Transform(_ context.Context, m hermod.Message, _ map[string]any) (hermod.Message, error) {
	return m, nil
}

func TestNamesListsWhatIsRegisteredSorted(t *testing.T) {
	r := NewRegistry()
	r.Register("zeta", nopTransformer{})
	r.Register("alpha", nopTransformer{})

	if got := r.Names(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("Names() = %v", got)
	}

	Register("names_test_probe", nopTransformer{})
	if !slices.Contains(Names(), "names_test_probe") {
		t.Fatalf("the default registry does not list what was registered: %v", Names())
	}
}
