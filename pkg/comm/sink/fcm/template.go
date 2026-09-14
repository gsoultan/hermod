package fcm

import (
	"bytes"
	"fmt"
	"maps"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/gsoultan/hermod"
)

// tmpl is a field that may be a Go template over the message.
//
// Templates are compiled once, in New, for two reasons. A broken template is
// then a refusal at save time rather than a failure on the first row, and a
// sink that may see thousands of messages a second does not re-parse the same
// text for each of them.
type tmpl struct {
	field  string
	raw    string
	parsed *template.Template
}

// compile prepares a templated field. Text with no action is kept as a literal
// so rendering it costs nothing.
func compile(field, raw string) (tmpl, error) {
	t := tmpl{field: field, raw: raw}
	if raw == "" || !strings.Contains(raw, "{{") {
		return t, nil
	}
	// missingkey=error turns a reference to a field the row does not have into
	// a failure that names the field, instead of Go's default: the literal
	// string "<no value>". A registration token of "<no value>" is a push sent
	// nowhere and an FCM refusal that says nothing about the cause.
	//
	// A field that is genuinely optional is reachable with {{index . "name"}},
	// which yields the zero value rather than an error.
	parsed, err := template.New(field).Option("missingkey=error").Parse(raw)
	if err != nil {
		return tmpl{}, fmt.Errorf("fcm sink: the %s template is not valid: %w", field, err)
	}
	t.parsed = parsed
	return t, nil
}

func (t tmpl) empty() bool { return t.raw == "" }

// fields is the set of top-level message fields this template reads.
//
// It exists so a destination template can say which of the row's columns is a
// routing capability rather than content: see destinationFields.
func (t tmpl) fields() []string {
	if t.parsed == nil || t.parsed.Tree == nil {
		return nil
	}
	seen := map[string]bool{}
	collectFields(t.parsed.Root, seen)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// collectFields walks a parsed template for field references. It handles the
// two ways a template names a field: `{{.name}}`, and the `{{index . "name"}}`
// form the docs point at for fields that may be absent.
func collectFields(node parse.Node, seen map[string]bool) {
	switch n := node.(type) {
	case nil:
		return
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, child := range n.Nodes {
			collectFields(child, seen)
		}
	case *parse.ActionNode:
		collectFields(n.Pipe, seen)
	case *parse.TemplateNode:
		collectFields(n.Pipe, seen)
	case *parse.PipeNode:
		collectPipe(n, seen)
	case *parse.CommandNode:
		collectCommand(n, seen)
	case *parse.FieldNode:
		if len(n.Ident) > 0 {
			seen[n.Ident[0]] = true
		}
	default:
		collectControlFlow(node, seen)
	}
}

// collectControlFlow handles the three nodes that carry a BranchNode. They are
// split out so the walker above stays one shape per case.
func collectControlFlow(node parse.Node, seen map[string]bool) {
	switch n := node.(type) {
	case *parse.IfNode:
		collectBranch(&n.BranchNode, seen)
	case *parse.RangeNode:
		collectBranch(&n.BranchNode, seen)
	case *parse.WithNode:
		collectBranch(&n.BranchNode, seen)
	}
}

func collectPipe(n *parse.PipeNode, seen map[string]bool) {
	if n == nil {
		return
	}
	for _, cmd := range n.Cmds {
		collectFields(cmd, seen)
	}
}

func collectCommand(n *parse.CommandNode, seen map[string]bool) {
	collectIndexArg(n, seen)
	for _, arg := range n.Args {
		collectFields(arg, seen)
	}
}

func collectBranch(b *parse.BranchNode, seen map[string]bool) {
	collectFields(b.Pipe, seen)
	collectFields(b.List, seen)
	collectFields(b.ElseList, seen)
}

// collectIndexArg reads the key out of `index . "name"`.
func collectIndexArg(cmd *parse.CommandNode, seen map[string]bool) {
	if len(cmd.Args) < 3 {
		return
	}
	ident, ok := cmd.Args[0].(*parse.IdentifierNode)
	if !ok || ident.Ident != "index" {
		return
	}
	if _, ok := cmd.Args[1].(*parse.DotNode); !ok {
		return
	}
	if key, ok := cmd.Args[2].(*parse.StringNode); ok {
		seen[key.Text] = true
	}
}

func (t tmpl) render(data map[string]any) (string, error) {
	if t.parsed == nil {
		return t.raw, nil
	}
	var buf bytes.Buffer
	if err := t.parsed.Execute(&buf, data); err != nil {
		return "", permanentf("fcm sink: rendering the %s template failed: %v", t.field, err)
	}
	return buf.String(), nil
}

// renderData is what a template sees.
//
// The envelope goes in first and the message's own fields are copied over the
// top, so a row with a column called `table` or `id` keeps its own value.
// Writing the envelope last is how a CDC row's `table` column was replaced by
// the envelope's table name in the preview path.
func renderData(msg hermod.Message) map[string]any {
	data := map[string]any{
		"id":        msg.ID(),
		"operation": string(msg.Operation()),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"metadata":  msg.Metadata(),
	}
	maps.Copy(data, msg.Data())
	return data
}
