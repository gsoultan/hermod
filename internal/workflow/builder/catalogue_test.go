package builder

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The catalogue is what the model is told it may build with. It is read
// from the registries, so it lists exactly the nodes this binary can run;
// these tests hold the two hand-written things around it to that list.

func TestEveryCatalogueKindIsDescribed(t *testing.T) {
	kinds := Catalogue()
	keys := map[string]bool{}
	for _, k := range kinds {
		keys[k.Key] = true
		if k.Description == "" {
			t.Errorf("node kind %q is registered but has no description in descriptions.go; "+
				"the model would be offered a node it is told nothing about", k.Key)
		}
	}
	for key := range descriptions {
		if !keys[key] {
			t.Errorf("descriptions.go describes %q, which is not a registered node kind", key)
		}
	}
}

func TestTheCatalogueOffersTheBasics(t *testing.T) {
	kinds := Catalogue()
	byKey := map[string]Kind{}
	for _, k := range kinds {
		byKey[k.Key] = k
	}
	for _, want := range []string{"source", "sink", "condition", "ai_classify", "transformation:ai_prompt", "transformation:set"} {
		if _, ok := byKey[want]; !ok {
			t.Errorf("the catalogue does not offer %q", want)
		}
	}
	if _, ok := byKey["transformation"]; ok {
		t.Error("the transformation dispatcher is offered as a node kind of its own")
	}
	if k := byKey["transformation:set"]; k.NodeType != "transformation" || k.TransType != "set" {
		t.Errorf("a transformer kind is %+v", k)
	}
}

// paletteNotOffered are editor palette entries the builder does not offer,
// with the reason. Anything else in the palette must be in the catalogue.
var paletteNotOffered = map[string]string{
	"merge":                   "an editor-only join point; the engine passes messages through it",
	"note":                    "a canvas annotation, not a processing step",
	"transformation:pipeline": "a composite of other steps; a draft lays the steps out as nodes instead",
}

var paletteItem = regexp.MustCompile(`\{\s*type:\s*'([a-z_0-9]+)'[^}]*?subType:\s*'([a-z_0-9]+)'`)

// TestTheEditorPaletteAndTheCatalogueAgree parses the UI's node palette and
// checks every node a person can drag onto the canvas can also be drafted.
func TestTheEditorPaletteAndTheCatalogueAgree(t *testing.T) {
	path := filepath.Join("..", "..", "..", "ui", "src", "pages", "workflows", "WorkflowEditor", "constants", "nodeCategories.ts")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the palette: %v", err)
	}
	matches := paletteItem.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 20 {
		t.Fatalf("found %d palette items; the parser no longer matches nodeCategories.ts", len(matches))
	}

	keys := map[string]bool{}
	for _, k := range Catalogue() {
		keys[k.Key] = true
	}
	var seen []string
	for _, m := range matches {
		typ, sub := m[1], m[2]
		key := typ
		if typ == TypeTransformation {
			key = TypeTransformation + ":" + sub
		}
		seen = append(seen, key)
		if keys[key] {
			continue
		}
		if _, ok := paletteNotOffered[key]; ok {
			continue
		}
		t.Errorf("the editor palette offers %q (subType %q) but the builder cannot draft it: "+
			"register it, or list it in paletteNotOffered with the reason", key, sub)
	}
	for key := range paletteNotOffered {
		if !slices.Contains(seen, key) {
			t.Errorf("paletteNotOffered lists %q, which is not in the palette any more", key)
		}
	}
}
