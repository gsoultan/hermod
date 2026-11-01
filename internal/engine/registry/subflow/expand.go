// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package subflow lets one workflow run another, reusable one in the middle of
// its graph.
//
// A sub-flow is an ordinary workflow that starts at a Sub-flow input node
// instead of a source and ends at one or more Return nodes. A Run sub-flow node
// in another workflow names it and a saved version of it.
//
// The call is resolved before the engine sees the workflow: Expand splices the
// pinned version's nodes into the caller's graph in place of the call node. A
// sub-flow therefore runs on the caller's message, in the caller's traversal,
// and inside the caller's acknowledgement — a source message is acknowledged
// only once every sink inside and outside the sub-flow has written. Everything
// the engine checks about a graph (commit together, Kafka exactly-once, cycles)
// is checked on the spliced one, so a sub-flow cannot be used to get around it.
package subflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/gsoultan/hermod/internal/storage"
)

// Node types. Input, Return and Call are what the editor draws; Enter and Exit
// are what Expand turns them into, and only the engine sees those.
const (
	TypeInput  = "subflow_input"
	TypeReturn = "subflow_return"
	TypeCall   = "call_subflow"
	TypeEnter  = "subflow_enter"
	TypeExit   = "subflow_exit"
)

// Separator joins a call node's ID to the IDs of the nodes spliced in for it.
// It is URL-safe, so a spliced node's ID can still go in an API path.
const Separator = "~"

// FramePrefix starts the metadata key under which a call holds its caller's
// message while the sub-flow runs; the call's ID follows it.
const FramePrefix = "_hermod_subflow:"

// WithoutFrames returns step, a message as a map (hermod.Message.ToMap), with
// any caller message a sub-flow call is holding taken out of its metadata. A
// trace step keeps the message each step left; without this every step inside
// a sub-flow would store the caller's message a second time. step is not
// changed.
func WithoutFrames(step map[string]any) map[string]any {
	var held bool
	switch md := step["metadata"].(type) {
	case map[string]string:
		for k := range md {
			held = held || strings.HasPrefix(k, FramePrefix)
		}
	case map[string]any:
		for k := range md {
			held = held || strings.HasPrefix(k, FramePrefix)
		}
	}
	if !held {
		return step
	}
	out := maps.Clone(step)
	switch md := step["metadata"].(type) {
	case map[string]string:
		cp := maps.Clone(md)
		maps.DeleteFunc(cp, func(k, _ string) bool { return strings.HasPrefix(k, FramePrefix) })
		out["metadata"] = cp
	case map[string]any:
		cp := maps.Clone(md)
		maps.DeleteFunc(cp, func(k string, _ any) bool { return strings.HasPrefix(k, FramePrefix) })
		out["metadata"] = cp
	}
	return out
}

// MaxDepth is how many sub-flows deep a call may nest.
const MaxDepth = 5

// errorExit is the ID suffix of the node that hands a failure inside the
// sub-flow back to the call's error edge.
const errorExit = "error"

// Loader reads a workflow at a saved version. Version 0 is the workflow as it is
// saved now.
type Loader func(ctx context.Context, id string, version int) (storage.Workflow, error)

// IsSubflow reports whether wf is a sub-flow: one with a Sub-flow input node.
func IsSubflow(wf storage.Workflow) bool {
	for _, n := range wf.Nodes {
		if n.Type == TypeInput {
			return true
		}
	}
	return false
}

// HasCalls reports whether wf calls a sub-flow.
func HasCalls(wf storage.Workflow) bool {
	for _, n := range wf.Nodes {
		if n.Type == TypeCall {
			return true
		}
	}
	return false
}

// Expand returns wf with every Run sub-flow node replaced by the nodes of the
// version it pins, recursively. wf itself is not changed. A workflow without
// calls is returned as it is.
func Expand(ctx context.Context, wf storage.Workflow, load Loader) (storage.Workflow, error) {
	if !HasCalls(wf) {
		return wf, nil
	}
	return expand(ctx, wf, load, []string{wf.ID})
}

func expand(ctx context.Context, wf storage.Workflow, load Loader, stack []string) (storage.Workflow, error) {
	out := wf
	out.Nodes = nil
	out.Edges = nil

	calls := make(map[string]storage.WorkflowNode)
	for _, n := range wf.Nodes {
		if n.Type == TypeCall {
			calls[n.ID] = n
			continue
		}
		out.Nodes = append(out.Nodes, n)
	}
	// Edges leaving a call are rebuilt by splice; every other edge, including
	// the ones into a call, stays as it is.
	leaving := make(map[string][]storage.WorkflowEdge)
	for _, e := range wf.Edges {
		if _, ok := calls[e.SourceID]; ok {
			leaving[e.SourceID] = append(leaving[e.SourceID], e)
			continue
		}
		out.Edges = append(out.Edges, e)
	}

	for _, n := range wf.Nodes {
		c, ok := calls[n.ID]
		if !ok {
			continue
		}
		nodes, edges, err := splice(ctx, c, leaving[c.ID], load, stack)
		if err != nil {
			return storage.Workflow{}, fmt.Errorf("sub-flow step %s: %w", nodeName(c), err)
		}
		out.Nodes = append(out.Nodes, nodes...)
		out.Edges = append(out.Edges, edges...)
	}
	return out, nil
}

// splice builds the nodes and edges that replace one call node.
func splice(ctx context.Context, c storage.WorkflowNode, leaving []storage.WorkflowEdge, load Loader, stack []string) ([]storage.WorkflowNode, []storage.WorkflowEdge, error) {
	id, sub, input, err := resolve(ctx, c, load, stack)
	if err != nil {
		return nil, nil, err
	}
	s := &spliced{call: c, prefix: c.ID + Separator, exits: map[string][]string{}, inner: map[string]bool{}}
	s.addNodes(sub, input)
	handlesError := s.addEdges(sub, input)
	errorTargets, err := s.wireLeaving(id, leaving)
	if err != nil {
		return nil, nil, err
	}
	if len(errorTargets) > 0 {
		s.routeErrors(handlesError, errorTargets)
	}
	return s.nodes, s.edges, nil
}

// resolve loads and checks the sub-flow a call pins, with its own calls
// expanded.
func resolve(ctx context.Context, c storage.WorkflowNode, load Loader, stack []string) (string, storage.Workflow, storage.WorkflowNode, error) {
	var none storage.Workflow
	var noInput storage.WorkflowNode
	id, _ := c.Config["subflow_id"].(string)
	if strings.TrimSpace(id) == "" {
		return "", none, noInput, errors.New("choose the sub-flow it runs")
	}
	version, err := pinnedVersion(c.Config["version"])
	if err != nil {
		return "", none, noInput, err
	}
	path := strings.Join(append(slices.Clone(stack), id), " > ")
	if slices.Contains(stack, id) {
		return "", none, noInput, fmt.Errorf("sub-flow %s calls itself (%s)", id, path)
	}
	if len(stack) > MaxDepth {
		return "", none, noInput, fmt.Errorf("sub-flows nest deeper than %d (%s)", MaxDepth, path)
	}
	if load == nil {
		return "", none, noInput, fmt.Errorf("sub-flow %s cannot be read here", id)
	}
	sub, err := load(ctx, id, version)
	if err != nil {
		return "", none, noInput, fmt.Errorf("sub-flow %s version %d: %w", id, version, err)
	}
	input, err := shape(sub)
	if err != nil {
		return "", none, noInput, fmt.Errorf("%s: %w", id, err)
	}
	if err := checkInputsMapped(c, input); err != nil {
		return "", none, noInput, err
	}
	sub, err = expand(ctx, sub, load, append(stack, id))
	if err != nil {
		return "", none, noInput, err
	}
	return id, sub, input, nil
}

// spliced collects what replaces one call node.
type spliced struct {
	call   storage.WorkflowNode
	prefix string
	nodes  []storage.WorkflowNode
	edges  []storage.WorkflowEdge
	exits  map[string][]string // return name -> exit node IDs
	all    []string            // every exit node ID, in order
	inner  map[string]bool     // IDs of the sub-flow's own nodes
}

// addNodes adds the enter node, which keeps the call's ID so the edges into
// the call still arrive, and the sub-flow's nodes under the call's prefix.
func (s *spliced) addNodes(sub storage.Workflow, input storage.WorkflowNode) {
	c := s.call
	enter := storage.WorkflowNode{ID: c.ID, Type: TypeEnter, RefID: c.RefID, X: c.X, Y: c.Y, Config: maps.Clone(c.Config)}
	if enter.Config == nil {
		enter.Config = map[string]any{}
	}
	enter.Config["call_id"] = c.ID
	enter.Config["fields"] = input.Config["fields"]
	s.nodes = append(s.nodes, enter)

	for _, n := range sub.Nodes {
		if n.ID == input.ID {
			continue
		}
		cp := n
		cp.ID = s.prefix + n.ID
		cp.Config = maps.Clone(n.Config)
		if cp.Config == nil {
			cp.Config = map[string]any{}
		}
		switch n.Type {
		case TypeReturn:
			name, _ := n.Config["name"].(string)
			cp.Type = TypeExit
			cp.Config["call_id"] = c.ID
			cp.Config["return"] = name
			cp.Config["outputs"] = c.Config["outputs"]
			s.exits[name] = append(s.exits[name], cp.ID)
			s.all = append(s.all, cp.ID)
		case TypeEnter, TypeExit:
			// A nested call: its call_id names a node that was just renamed.
			if callID, ok := cp.Config["call_id"].(string); ok {
				cp.Config["call_id"] = s.prefix + callID
			}
		}
		s.nodes = append(s.nodes, cp)
		s.inner[cp.ID] = true
	}
}

// addEdges adds the sub-flow's edges under the call's prefix, and reports the
// nodes that handle their own errors.
func (s *spliced) addEdges(sub storage.Workflow, input storage.WorkflowNode) map[string]bool {
	handlesError := make(map[string]bool)
	for _, e := range sub.Edges {
		cp := e
		cp.ID = s.prefix + e.ID
		cp.SourceID = rename(e.SourceID, input.ID, s.call.ID, s.prefix)
		cp.TargetID = rename(e.TargetID, input.ID, s.call.ID, s.prefix)
		if edgeLabel(e) == errorExit {
			handlesError[cp.SourceID] = true
		}
		s.edges = append(s.edges, cp)
	}
	return handlesError
}

// wireLeaving moves each edge out of the call onto the exits it names, and
// returns the call's error edges.
func (s *spliced) wireLeaving(id string, leaving []storage.WorkflowEdge) ([]storage.WorkflowEdge, error) {
	var errorTargets []storage.WorkflowEdge
	for _, e := range leaving {
		label := edgeLabel(e)
		if label == errorExit {
			// The call itself failing — an input that is missing — leaves the
			// caller's message as it was, so it can go straight to the error
			// edge.
			s.edges = append(s.edges, e)
			errorTargets = append(errorTargets, e)
			continue
		}
		from := s.all
		if label != "" && len(s.all) > 1 {
			from = s.exits[label]
		}
		if len(from) == 0 {
			return nil, fmt.Errorf("sub-flow %s has no return named %q", id, label)
		}
		for _, x := range from {
			cp := withoutLabel(e)
			cp.ID = e.ID + Separator + strings.TrimPrefix(x, s.prefix)
			cp.SourceID = x
			s.edges = append(s.edges, cp)
		}
	}
	return errorTargets, nil
}

// routeErrors sends a failure inside the sub-flow that the sub-flow does not
// handle itself to one exit that restores the caller's message and then takes
// the call's error edges.
func (s *spliced) routeErrors(handlesError map[string]bool, errorTargets []storage.WorkflowEdge) {
	restore := storage.WorkflowNode{
		ID: s.prefix + errorExit, Type: TypeExit, X: s.call.X, Y: s.call.Y,
		Config: map[string]any{"call_id": s.call.ID, "on_error": true},
	}
	for _, n := range s.nodes {
		if !s.inner[n.ID] || handlesError[n.ID] || n.Type == TypeExit {
			continue
		}
		s.edges = append(s.edges, storage.WorkflowEdge{
			ID: n.ID + Separator + errorExit, SourceID: n.ID, TargetID: restore.ID, SourceHandle: errorExit,
		})
	}
	s.nodes = append(s.nodes, restore)
	for _, e := range errorTargets {
		cp := withoutLabel(e)
		cp.ID = e.ID + Separator + errorExit
		cp.SourceID = restore.ID
		s.edges = append(s.edges, cp)
	}
}

// CheckShape requires wf to be a sub-flow: one Sub-flow input step, at least
// one Return step, and no source.
func CheckShape(wf storage.Workflow) error {
	_, err := shape(wf)
	return err
}

func shape(sub storage.Workflow) (storage.WorkflowNode, error) {
	var input storage.WorkflowNode
	inputs, returns, sources := 0, 0, 0
	for _, n := range sub.Nodes {
		switch n.Type {
		case TypeInput:
			input = n
			inputs++
		case TypeReturn:
			returns++
		case "source":
			sources++
		}
	}
	switch {
	case inputs == 0:
		return input, errors.New("not a sub-flow: it has no Sub-flow input step")
	case inputs > 1:
		return input, fmt.Errorf("a sub-flow has exactly one Sub-flow input step, this has %d", inputs)
	case sources > 0:
		return input, errors.New("a sub-flow cannot have a source; it gets its data from the step that calls it")
	case returns == 0:
		return input, errors.New("a sub-flow needs at least one Return step")
	}
	return input, nil
}

// checkInputsMapped requires every required input of the sub-flow to be fed by
// the call.
func checkInputsMapped(c storage.WorkflowNode, input storage.WorkflowNode) error {
	mapped := make(map[string]bool)
	for _, m := range Mappings(c.Config["inputs"]) {
		mapped[m.To] = true
	}
	for _, f := range Fields(input.Config["fields"]) {
		if f.Required && !mapped[f.Name] {
			return fmt.Errorf("required sub-flow input %q is not mapped", f.Name)
		}
	}
	return nil
}

// pinnedVersion reads a call's version: a saved version number, or "latest"
// for the workflow as it is saved now.
func pinnedVersion(v any) (int, error) {
	switch v := v.(type) {
	case float64:
		if v >= 1 && v == float64(int(v)) {
			return int(v), nil
		}
	case int:
		if v >= 1 {
			return v, nil
		}
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "latest") {
			return 0, nil
		}
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 1 {
			return n, nil
		}
	}
	return 0, errors.New("pin the sub-flow version it runs")
}

func rename(id, inputID, callID, prefix string) string {
	if id == inputID {
		return callID
	}
	return prefix + id
}

func withoutLabel(e storage.WorkflowEdge) storage.WorkflowEdge {
	cp := e
	cp.SourceHandle = ""
	if _, ok := e.Config["label"]; ok {
		cp.Config = maps.Clone(e.Config)
		delete(cp.Config, "label")
	}
	return cp
}

// edgeLabel is the branch an edge carries: its configured label, else its
// source handle. It is the engine's rule (registry.edgeLabel).
func edgeLabel(e storage.WorkflowEdge) string {
	if l, ok := e.Config["label"].(string); ok && l != "" {
		return l
	}
	return e.SourceHandle
}

func nodeName(n storage.WorkflowNode) string {
	if l, ok := n.Config["label"].(string); ok && l != "" {
		return fmt.Sprintf("%q", l)
	}
	return n.ID
}

// Mapping copies the value at From to To.
type Mapping struct {
	From string
	To   string
}

// Mappings reads a call's inputs or outputs: a list of {"from", "to"}.
func Mappings(v any) []Mapping {
	list, _ := v.([]any)
	out := make([]Mapping, 0, len(list))
	for _, item := range list {
		m, _ := item.(map[string]any)
		from, _ := m["from"].(string)
		to, _ := m["to"].(string)
		from, to = strings.TrimSpace(from), strings.TrimSpace(to)
		if from == "" || to == "" {
			continue
		}
		out = append(out, Mapping{From: from, To: to})
	}
	return out
}

// Field is one input a sub-flow declares.
type Field struct {
	Name     string
	Required bool
}

// Fields reads a Sub-flow input node's fields: a list of {"name", "required"}.
func Fields(v any) []Field {
	list, _ := v.([]any)
	out := make([]Field, 0, len(list))
	for _, item := range list {
		m, _ := item.(map[string]any)
		name, _ := m["name"].(string)
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		req, _ := m["required"].(bool)
		out = append(out, Field{Name: name, Required: req})
	}
	return out
}
