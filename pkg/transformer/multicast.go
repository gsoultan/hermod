package transformer

import (
	"context"
	"fmt"

	"github.com/user/hermod"
)

// MulticastTransformer explicitly clones the current message into an array under _fanout
// with optional shallow projections per branch.
// Config:
// - branches: [ { select: ["a","b"], prefix: "x_" }, { } ]
//   - select: optional list of keys to keep
//   - prefix: optional prefix to add to keys
//
// Result is written to targetField (default: _fanout)
func init() { Register("multicast", &MulticastTransformer{}) }

type MulticastTransformer struct{}

func (t *MulticastTransformer) Transform(ctx context.Context, msg hermod.Message, config map[string]interface{}) (hermod.Message, error) {
	if msg == nil {
		return nil, nil
	}
	branches, _ := config["branches"].([]interface{})
	if len(branches) == 0 {
		// default: clone once
		branches = []interface{}{map[string]interface{}{}}
	}
	targetField, _ := config["targetField"].(string)
	if targetField == "" {
		targetField = "_fanout"
	}

	src := msg.Data()
	out := make([]interface{}, 0, len(branches))
	for _, br := range branches {
		bm, _ := br.(map[string]interface{})
		// Selection
		selected := map[string]interface{}{}
		if sel, ok := bm["select"].([]interface{}); ok && len(sel) > 0 {
			for _, k := range sel {
				ks := toString(k)
				if v, ok := src[ks]; ok {
					selected[ks] = v
				}
			}
		} else {
			for k, v := range src {
				selected[k] = v
			}
		}
		// Prefix
		if pfx, _ := bm["prefix"].(string); pfx != "" {
			withPfx := make(map[string]interface{}, len(selected))
			for k, v := range selected {
				withPfx[pfx+k] = v
			}
			selected = withPfx
		}
		out = append(out, selected)
	}
	msg.SetData(targetField, out)
	return msg, nil
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}
