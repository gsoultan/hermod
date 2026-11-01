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
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/engine/registry/interfaces"
	"github.com/gsoultan/hermod/internal/engine/registry/subflow"
	"github.com/gsoultan/hermod/internal/storage"
	msgpkg "github.com/gsoultan/hermod/pkg/comm/message"
)

func enterNode() *storage.WorkflowNode {
	return &storage.WorkflowNode{ID: "c", Type: subflow.TypeEnter, Config: map[string]any{
		"call_id": "c",
		"fields":  []any{map[string]any{"name": "zip", "required": true}, map[string]any{"name": "street"}},
		"inputs": []any{
			map[string]any{"from": "customer.zip", "to": "zip"},
			map[string]any{"from": "customer.street", "to": "street"},
		},
	}}
}

func exitNode(cfg map[string]any) *storage.WorkflowNode {
	c := map[string]any{"call_id": "c", "return": "valid"}
	for k, v := range cfg {
		c[k] = v
	}
	return &storage.WorkflowNode{ID: "c~ok", Type: subflow.TypeExit, Config: c}
}

func orderMessage() *msgpkg.DefaultMessage {
	m := msgpkg.AcquireMessage()
	m.SetID("order-1")
	m.SetData("order_id", float64(88412))
	m.SetData("customer", map[string]any{"zip": "10115", "street": "Main 1", "email": "a@b.c"})
	return m
}

func run(t *testing.T, n *storage.WorkflowNode, m hermod.Message) (hermod.Message, string) {
	t.Helper()
	exec, ok := interfaces.GetNodeExecutor(n.Type)
	if !ok {
		t.Fatalf("no executor for %s", n.Type)
	}
	out, branch, err := exec.Execute(context.Background(), newJoinCtx(), "wf", n, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d messages, want 1", len(out))
	}
	return out[0], branch
}

func TestSubflowEnterGivesTheSubflowOnlyItsInputs(t *testing.T) {
	m := orderMessage()
	defer m.Release()
	out, _ := run(t, enterNode(), m)

	if got, want := out.Data(), map[string]any{"zip": "10115", "street": "Main 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sub-flow sees %v, want %v", got, want)
	}
	if got := out.Metadata()["idempotency_key"]; got != "order-1:c" {
		t.Fatalf("idempotency key inside the sub-flow = %q, want order-1:c", got)
	}
}

func TestSubflowExitRestoresTheCallerAndMapsOutputs(t *testing.T) {
	m := orderMessage()
	m.SetMetadata("idempotency_key", "pk-7")
	defer m.Release()
	inside, _ := run(t, enterNode(), m)
	inside.SetData("normalized", "Main Street 1, 10115 Berlin")

	out, branch := run(t, exitNode(map[string]any{
		"outputs": []any{map[string]any{"from": "normalized", "to": "customer.address"}},
	}), inside)

	if branch != "" {
		t.Fatalf("branch = %q, want none", branch)
	}
	cust, _ := out.Data()["customer"].(map[string]any)
	if cust["address"] != "Main Street 1, 10115 Berlin" || cust["email"] != "a@b.c" || out.Data()["order_id"] != float64(88412) {
		t.Fatalf("caller after the sub-flow = %v", out.Data())
	}
	if _, leaked := out.Data()["normalized"]; leaked {
		t.Fatal("an unmapped sub-flow field reached the caller")
	}
	md := out.Metadata()
	if md["idempotency_key"] != "pk-7" {
		t.Fatalf("idempotency key after the sub-flow = %q, want the caller's pk-7", md["idempotency_key"])
	}
	for k := range md {
		if strings.HasPrefix(k, "_hermod_subflow") {
			t.Fatalf("frame %s left on the message", k)
		}
	}
}

func TestSubflowExitWithoutOutputsKeepsTheResultUnderSubflow(t *testing.T) {
	m := orderMessage()
	defer m.Release()
	inside, _ := run(t, enterNode(), m)
	out, _ := run(t, exitNode(nil), inside)
	got, _ := out.Data()["subflow"].(map[string]any)
	if got["zip"] != "10115" {
		t.Fatalf("result = %v, want the sub-flow's data under subflow", out.Data())
	}
}

func TestSubflowErrorExitRestoresTheCallerUnchanged(t *testing.T) {
	m := orderMessage()
	defer m.Release()
	inside, _ := run(t, enterNode(), m)
	inside.SetData("half_done", true)
	out, _ := run(t, exitNode(map[string]any{"on_error": true, "outputs": []any{map[string]any{"from": "half_done", "to": "x"}}}), inside)
	if _, ok := out.Data()["x"]; ok {
		t.Fatal("a failed sub-flow mapped its outputs")
	}
	if out.Data()["order_id"] != float64(88412) {
		t.Fatalf("caller after a failed sub-flow = %v", out.Data())
	}
}

func TestSubflowEnterRefusesAMissingRequiredInput(t *testing.T) {
	m := msgpkg.AcquireMessage()
	m.SetID("x")
	m.SetData("customer", map[string]any{"street": "Main 1"})
	defer m.Release()
	exec, _ := interfaces.GetNodeExecutor(subflow.TypeEnter)
	_, _, err := exec.Execute(context.Background(), newJoinCtx(), "wf", enterNode(), m)
	if err == nil || !strings.Contains(err.Error(), `"zip"`) {
		t.Fatalf("err = %v, want the missing input named", err)
	}
	if m.Data()["customer"] == nil {
		t.Fatal("a refused call changed the caller's message")
	}
}

func TestSubflowExitWithoutAFrameFails(t *testing.T) {
	m := orderMessage()
	defer m.Release()
	exec, _ := interfaces.GetNodeExecutor(subflow.TypeExit)
	if _, _, err := exec.Execute(context.Background(), newJoinCtx(), "wf", exitNode(nil), m); err == nil {
		t.Fatal("an exit reached without its enter returned no error")
	}
}

func TestAnUnexpandedCallFails(t *testing.T) {
	m := orderMessage()
	defer m.Release()
	exec, ok := interfaces.GetNodeExecutor(subflow.TypeCall)
	if !ok {
		t.Fatal("a call node with no executor would pass messages through untouched")
	}
	if _, _, err := exec.Execute(context.Background(), newJoinCtx(), "wf", &storage.WorkflowNode{ID: "c", Type: subflow.TypeCall}, m); err == nil {
		t.Fatal("an unexpanded call ran")
	}
}

func TestNestedCallsKeepSeparateFrames(t *testing.T) {
	m := orderMessage()
	defer m.Release()
	outer, _ := run(t, enterNode(), m)
	innerEnter := &storage.WorkflowNode{ID: "c~g", Type: subflow.TypeEnter, Config: map[string]any{
		"call_id": "c~g", "inputs": []any{map[string]any{"from": "zip", "to": "postcode"}},
	}}
	inner, _ := run(t, innerEnter, outer)
	if got := inner.Metadata()["idempotency_key"]; got != "order-1:c:c~g" {
		t.Fatalf("nested idempotency key = %q", got)
	}
	back, _ := run(t, &storage.WorkflowNode{ID: "c~g~r", Type: subflow.TypeExit, Config: map[string]any{"call_id": "c~g"}}, inner)
	if back.Data()["zip"] != "10115" {
		t.Fatalf("outer sub-flow after the inner one = %v", back.Data())
	}
	final, _ := run(t, exitNode(nil), back)
	if final.Data()["order_id"] != float64(88412) || final.Metadata()["idempotency_key"] != "" {
		t.Fatalf("caller after both = %v %v", final.Data(), final.Metadata())
	}
}

func TestSubflowKeepsALargeIntegerExact(t *testing.T) {
	m := orderMessage()
	m.SetData("big", int64(1<<53+1))
	defer m.Release()
	inside, _ := run(t, enterNode(), m)
	out, _ := run(t, exitNode(nil), inside)
	if got := out.Data()["big"]; got != int64(1<<53+1) {
		t.Fatalf("big = %v (%T), want 9007199254740993", got, got)
	}
}

func TestSubflowCallOnAnEmptyMessage(t *testing.T) {
	m := msgpkg.AcquireMessage()
	m.SetID("empty")
	defer m.Release()
	n := enterNode()
	n.Config["fields"] = nil
	inside, _ := run(t, n, m)
	inside.SetData("made", "here")
	out, _ := run(t, exitNode(nil), inside)
	got, _ := out.Data()["subflow"].(map[string]any)
	if got["made"] != "here" {
		t.Fatalf("result on an empty caller = %v", out.Data())
	}
}
