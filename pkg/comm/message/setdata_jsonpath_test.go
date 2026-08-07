package message

import (
	"testing"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/infra/evaluator"
)

// Every transformer that writes a result takes a field path from the user, and
// the UI teaches JSONPath ("$.customer_name"). The read side strips that root
// prefix (evaluator.GetMsgValByPath); the write side used to split the path on
// "." with no knowledge of "$", so SetData("$.customer_name", v) buried the
// value under a literal "$" key:
//
//	{"$": {"customer_name": "Customer 2"}, "customer_code": "C0002"}
//
// Nothing errored. The enrichment simply was not where the sink, the next node
// or the user looked for it — observed end to end in a CDC → db_lookup →
// Postgres sink run.
func TestSetDataRoundTripsJSONPathRoot(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string // where the value must be readable from
	}{
		{"json path root", "$.customer_name", "customer_name"},
		{"json path nested", "$.enrich.customer_name", "enrich.customer_name"},
		{"plain field", "customer_name", "customer_name"},
		{"plain nested", "enrich.customer_name", "enrich.customer_name"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := AcquireMessage()
			defer ReleaseMessage(msg)
			msg.SetPayload([]byte(`{"customer_code":"C0002"}`))

			msg.SetData(tc.path, "Customer 2")

			// Written path and read path must agree.
			if got := evaluator.GetMsgValByPath(msg, tc.path); got != "Customer 2" {
				t.Errorf("reading back the path just written: got %v, want %q", got, "Customer 2")
			}
			// And it must land where a sink or a downstream node would look.
			if got := evaluator.GetMsgValByPath(msg, tc.want); got != "Customer 2" {
				t.Errorf("value not at %q: got %v", tc.want, got)
			}
			// A literal "$" key means the prefix was treated as a field name.
			if _, buried := msg.Data()["$"]; buried {
				t.Errorf(`value buried under a literal "$" key: %v`, msg.Data())
			}
		})
	}
}

// The root prefix on its own addresses the whole document, not a field called
// "$"; writing to it must not create one.
func TestSetDataBareRootPrefix(t *testing.T) {
	msg := AcquireMessage()
	defer ReleaseMessage(msg)
	msg.SetPayload([]byte(`{"a":1}`))

	msg.SetData("$.value", 42)

	if _, buried := msg.Data()["$"]; buried {
		t.Fatalf(`literal "$" key created: %v`, msg.Data())
	}
	var _ hermod.Message = msg
}
