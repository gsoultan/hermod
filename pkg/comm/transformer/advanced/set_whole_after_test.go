// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package advanced_test

import (
	"encoding/json"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/evaluator"
)

// `column.after = source.after._item` after a foreach is how the editor turns
// each array element into the row. It stored a literal "after" key next to the
// old columns instead, and that key then *was* the after-image: every field a
// later node wrote -- a db_lookup's targetField -- went to the root beside it
// and never serialised. The lookup reported a hit, and the next node and the
// sink saw no trace of what it found.
func TestSetWholeAfterReplacesTheRow(t *testing.T) {
	for _, prepared := range []bool{true, false} {
		msg := message.AcquireMessage()
		t.Cleanup(msg.Release)
		msg.SetOperation(hermod.OpSnapshot)
		msg.SetBefore([]byte(`{"org_name":"old"}`))
		msg.SetAfter([]byte(`{"items":[{"org_name":"acme"}],"_item":{"org_name":"acme"},"_index":0}`))

		out := runSetNode(t, map[string]any{"transType": "set", "column.after": "source.after._item"}, msg, prepared)
		// What a db_lookup with targetField "osiris" does on a hit.
		out.SetData("osiris", map[string]any{"OrganizationCode": "IMPC"})

		var image map[string]any
		if err := json.Unmarshal(out.Payload(), &image); err != nil {
			t.Fatalf("prepared=%v: payload %s: %v", prepared, out.Payload(), err)
		}
		if image["org_name"] != "acme" {
			t.Errorf("prepared=%v: the row is not the item: %v", prepared, image)
		}
		if _, stale := image["items"]; stale {
			t.Errorf("prepared=%v: the replaced row's columns survived: %v", prepared, image)
		}
		if got := evaluator.GetMsgValByPath(out, "after.osiris.OrganizationCode"); got != "IMPC" {
			t.Errorf("prepared=%v: after.osiris.OrganizationCode = %#v, want IMPC; after-image %v", prepared, got, image)
		}
		if string(out.Before()) != `{"org_name":"old"}` {
			t.Errorf("prepared=%v: before-image = %s, want it untouched", prepared, out.Before())
		}
	}
}
