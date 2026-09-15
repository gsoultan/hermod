package core

import (
	"context"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
)

func cdcOrder(t *testing.T) hermod.Message {
	t.Helper()
	m := message.AcquireMessage()
	m.SetOperation(hermod.OpCreate)
	m.SetTable("orders")
	m.SetAfter([]byte(`{"id":"1","amount":"10.50","qty":"3"}`))
	return m
}

// TestConversionReportsWhatItDid records what data_conversion does for each of
// the ways an operator can get it wrong, so "the node ran and nothing changed"
// is distinguishable from "the node ran and refused".
func TestConversionReportsWhatItDid(t *testing.T) {
	tr := &DataConversionTransformer{}
	ctx := context.Background()

	cases := []struct {
		name   string
		config map[string]any
	}{
		{"field does not exist (typo)", map[string]any{
			"field": "no_such_field", "targetType": "float"}},
		{"missing field, errorBehavior=keep", map[string]any{
			"field": "no_such_field", "targetType": "float", "errorBehavior": "keep"}},
		{"missing field, errorBehavior=null", map[string]any{
			"field": "no_such_field", "targetType": "float", "errorBehavior": "null"}},
		{"field exists, target type omitted", map[string]any{
			"field": "amount"}},
		{"field exists, target type misspelled", map[string]any{
			"field": "amount", "targetType": "flaot"}},
		{"decimal string to int", map[string]any{
			"field": "amount", "targetType": "int"}},
		{"good conversion (control)", map[string]any{
			"field": "amount", "targetType": "float"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := cdcOrder(t)
			before := string(msg.Payload())

			out, err := tr.Transform(ctx, msg, tc.config)

			after := "<nil message>"
			if out != nil {
				after = string(out.Payload())
			}
			changed := out != nil && after != before

			t.Logf("err=%v  changed=%v\n  before = %s\n  after  = %s",
				err, changed, before, after)

			// "keep" is the one configuration where doing nothing is the
			// documented answer: the operator has said an absent field is fine.
			if tc.config["errorBehavior"] == "keep" {
				if err != nil {
					t.Errorf("errorBehavior=keep should pass the message through, got %v", err)
				}
				return
			}

			if err == nil && !changed {
				t.Errorf("the node reported success and changed nothing: an operator "+
					"sees a green node and unchanged data, with no way to tell it did not run "+
					"(config %v)", tc.config)
			}
		})
	}
}
