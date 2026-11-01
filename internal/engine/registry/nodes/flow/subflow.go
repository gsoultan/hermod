// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/engine/registry/subflow"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

func init() {
	interfaces.RegisterNodeExecutor(subflow.TypeEnter, &SubflowEnterNode{})
	interfaces.RegisterNodeExecutor(subflow.TypeExit, &SubflowExitNode{})
	interfaces.RegisterNodeExecutor(subflow.TypeCall, &unexpandedCallNode{})
}

// frameKey is the metadata key that holds the caller's message while a
// sub-flow runs on the same message. Metadata, because it travels with the
// message wherever the message goes — through a branch, a retry, or a Wait that
// suspends it to the database — and no sink copies metadata it does not name.
const frameKey = subflow.FramePrefix

const idempotencyKey = "idempotency_key"

// maxExactFloat is the largest integer a float64 holds exactly.
const maxExactFloat = 1 << 53

// frame is what a sub-flow call saves of the caller's message.
type frame struct {
	Data    map[string]any `json:"data"`
	Before  []byte         `json:"before,omitempty"`
	Idem    string         `json:"idem,omitempty"`
	HadIdem bool           `json:"had_idem,omitempty"`
}

// SubflowEnterNode starts a sub-flow call: it saves the caller's message and
// leaves the sub-flow only the inputs the call maps to it.
//
// Inside the sub-flow the idempotency key is the caller's key plus the call's
// ID, so a sink inside a sub-flow writes its own row for each call, and a
// redelivery overwrites the row it wrote the first time.
type SubflowEnterNode struct{}

func (n *SubflowEnterNode) Execute(_ context.Context, _ interfaces.NodeContext, _ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	callID := callIDOf(node)
	inputs := make(map[string]any)
	for _, m := range subflow.Mappings(node.Config["inputs"]) {
		if v := evaluator.GetMsgValByPath(msg, m.From); v != nil {
			inputs[m.To] = v
		}
	}
	// Checked before anything changes, so a refused call leaves the caller's
	// message as it was for the call's error edge.
	for _, f := range subflow.Fields(node.Config["fields"]) {
		if _, ok := inputs[f.Name]; f.Required && !ok {
			return nil, "", fmt.Errorf("sub-flow input %q is required and the message has no value for it", f.Name)
		}
	}

	idem, hadIdem := hermod.MetadataValue(msg, idempotencyKey)
	saved, err := json.Marshal(frame{Data: msg.Data(), Before: msg.Before(), Idem: idem, HadIdem: hadIdem})
	if err != nil {
		return nil, "", fmt.Errorf("sub-flow call %s: saving the message: %w", callID, err)
	}

	base := idem
	if !hadIdem || strings.TrimSpace(base) == "" {
		base = msg.ID()
	}
	msg.SetMetadata(frameKey+callID, string(saved))
	msg.SetMetadata(idempotencyKey, base+":"+callID)
	replaceData(msg, inputs)
	return []hermod.Message{msg}, "", nil
}

// SubflowExitNode ends a sub-flow call at a Return step: it restores the
// caller's message and copies the call's outputs into it. With on_error set it
// restores the caller's message and copies nothing, so a failure inside the
// sub-flow reaches the call's error edge with the message the caller sent.
type SubflowExitNode struct{}

func (n *SubflowExitNode) Execute(_ context.Context, _ interfaces.NodeContext, _ string, node *storage.WorkflowNode, msg hermod.Message) ([]hermod.Message, string, error) {
	callID := callIDOf(node)
	saved, ok := hermod.MetadataValue(msg, frameKey+callID)
	if !ok {
		return nil, "", fmt.Errorf("sub-flow call %s returned a message it never received", callID)
	}
	var f frame
	dec := json.NewDecoder(strings.NewReader(saved))
	dec.UseNumber()
	if err := dec.Decode(&f); err != nil {
		return nil, "", fmt.Errorf("sub-flow call %s: reading the saved message: %w", callID, err)
	}

	result := msg.Data()
	onError, _ := node.Config["on_error"].(bool)

	// A caller with an empty body saved null.
	caller, _ := numbers(f.Data).(map[string]any)
	replaceData(msg, caller)
	if len(f.Before) > 0 {
		if b, ok := msg.(interface{ SetBefore([]byte) }); ok {
			b.SetBefore(f.Before)
		}
	}
	if !onError {
		outputs := subflow.Mappings(node.Config["outputs"])
		if len(outputs) == 0 {
			msg.SetData("subflow", result)
		}
		for _, m := range outputs {
			if v := evaluator.GetValByPath(result, m.From); v != nil {
				msg.SetData(m.To, v)
			}
		}
	}

	deleteMetadata(msg, frameKey+callID)
	if f.HadIdem {
		msg.SetMetadata(idempotencyKey, f.Idem)
	} else {
		deleteMetadata(msg, idempotencyKey)
	}
	return []hermod.Message{msg}, "", nil
}

// unexpandedCallNode runs a Run sub-flow node the engine never spliced. That
// is a bug, and passing the message through untouched — the engine's default
// for a type with no executor — would hide it behind a workflow that seems to
// work.
type unexpandedCallNode struct{}

func (n *unexpandedCallNode) Execute(_ context.Context, _ interfaces.NodeContext, _ string, node *storage.WorkflowNode, _ hermod.Message) ([]hermod.Message, string, error) {
	return nil, "", errors.New("sub-flow step " + node.ID + " was not expanded before the workflow ran")
}

func callIDOf(node *storage.WorkflowNode) string {
	if id, ok := node.Config["call_id"].(string); ok && id != "" {
		return id
	}
	return node.ID
}

// replaceData makes data the message's whole body.
func replaceData(msg hermod.Message, data map[string]any) {
	msg.ClearPayloads()
	ref := msg.DataRef()
	clear(ref)
	for k, v := range data {
		msg.SetData(k, v)
	}
}

func deleteMetadata(msg hermod.Message, key string) {
	if d, ok := msg.(interface{ DeleteMetadata(string) }); ok {
		d.DeleteMetadata(key)
		return
	}
	msg.SetMetadata(key, "")
}

// numbers turns the json.Number values a saved message decodes to back into
// float64, which is what a message decoded from JSON carries — except an
// integer too large for a float64 to hold exactly, which stays an int64 so an
// id above 2^53 is not rounded.
func numbers(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			v[k] = numbers(x)
		}
		return v
	case []any:
		for i, x := range v {
			v[i] = numbers(x)
		}
		return v
	case json.Number:
		if i, err := v.Int64(); err == nil && !bytes.ContainsAny([]byte(v), ".eE") && (i > maxExactFloat || i < -maxExactFloat) {
			return i
		}
		f, _ := v.Float64()
		return f
	case nil:
		return v
	}
	return v
}
