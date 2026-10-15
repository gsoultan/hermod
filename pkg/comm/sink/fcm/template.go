// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package fcm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
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
	// field is the configuration key the template is stored under, and entry
	// the data key for one of the App data values.
	field  string
	entry  string
	raw    string
	parsed *template.Template
}

// noValue is what text/template prints for a nil value: a column that is null
// in this row, or an optional field read with index that the row does not
// have. It is never what anyone meant to send.
const noValue = "<no value>"

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
	parsed, err := template.New(field).Option("missingkey=error").Funcs(funcs).Parse(raw)
	if err != nil {
		return tmpl{}, &FieldError{
			Key:      field,
			Template: raw,
			Problem:  fmt.Sprintf("is not a valid template: %v", err),
			Fix:      `a field is written {{.column}}; every {{ needs a matching }}`,
		}
	}
	t.parsed = parsed
	return t, nil
}

// funcs are the functions a template may call.
//
// json writes a value the way "All fields" carries it: text as it is, an
// object or a list as JSON, null as nothing. Without it an object column picked
// under "Selected fields" arrived as Go's map[k:v].
var funcs = template.FuncMap{
	"json": stringify,
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
		switch {
		case len(n.Ident) > 1 && (n.Ident[0] == "after" || n.Ident[0] == "before"):
			// `.after.fcm_token` reads the fcm_token column through the
			// change event's image; the column is the second word.
			seen[n.Ident[1]] = true
		case len(n.Ident) > 0:
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

// render fills the template in from one row.
//
// A null column renders as nothing. Go prints it as "<no value>", and that used
// to be what was sent: as a lock-screen title, and as a registration token —
// which FCM refused as INVALID_ARGUMENT without saying which value it meant.
// An empty token is now refused by name before anything is sent.
func (t tmpl) render(data map[string]any) (string, error) {
	if t.parsed == nil {
		return t.raw, nil
	}
	var buf bytes.Buffer
	if err := t.parsed.Execute(&buf, data); err != nil {
		problem := "could not be filled in from this row: " + execReason(err)
		if m := missingKey.FindStringSubmatch(err.Error()); m != nil {
			// A null column is left out of the row altogether, so to the
			// template "null" and "no such column" are the same thing.
			problem = fmt.Sprintf("reads the field %q, which is missing or null in this row", m[1])
		}
		if t.entry != "" {
			problem = fmt.Sprintf("key %q %s", t.entry, problem)
		}
		return "", &FieldError{
			Key:      t.field,
			Template: t.raw,
			Problem:  problem,
			Fix: fmt.Sprintf(`pick a field this row has (%s) with the { } button, or write {{index . "name"}} for a field that may be missing`,
				rowKeys(data)),
		}
	}
	return strings.ReplaceAll(buf.String(), noValue, ""), nil
}

// missingKey finds the field in text/template's "map has no entry for key".
var missingKey = regexp.MustCompile(`map has no entry for key "([^"]*)"`)

// execReason is the part of a template execution error a reader can use. Go's
// own carries the template name and a parse position in front of it.
func execReason(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 && strings.Contains(msg, "executing") {
		return msg[i+2:]
	}
	return msg
}

// renderData is what a template sees.
//
// The envelope goes in first and the row's own fields are copied over the top,
// so a row with a column called `table` or `id` keeps its own value. Writing
// the envelope last is how a CDC row's `table` column was replaced by the
// envelope's table name in the preview path.
//
// `after` and `before` are the change event's two images, and `meta` is
// `metadata` under the name the rest of Hermod uses. The editor offers a CDC
// row's columns as `after.<column>` because that is the shape of the sample it
// captured; without these keys every such field failed every message.
func renderData(msg hermod.Message) map[string]any {
	metadata := msg.Metadata()
	after := msg.Data()
	before := imageOf(msg.Before())
	data := map[string]any{
		"id":        msg.ID(),
		"operation": string(msg.Operation()),
		"table":     msg.Table(),
		"schema":    msg.Schema(),
		"metadata":  metadata,
		"meta":      metadata,
		"after":     after,
	}
	if before != nil {
		data["before"] = before
	}
	maps.Copy(data, pickRow(after, before))
	return data
}

// rowOf is the row a message is about: see pickRow.
func rowOf(msg hermod.Message) map[string]any {
	row := msg.Data()
	if len(row) > 0 {
		return row
	}
	return imageOf(msg.Before())
}

// pickRow is the data map, or for a delete — which carries its row only as a
// before-image, so its data map is empty — the before-image.
func pickRow(after, before map[string]any) map[string]any {
	if len(after) > 0 {
		return after
	}
	return before
}

// imageOf decodes a change event's image. Nil when there is none or it is not
// a JSON object, which leaves a template reading it to fail and say so.
func imageOf(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var image map[string]any
	if err := json.Unmarshal(raw, &image); err != nil {
		return nil
	}
	return image
}
