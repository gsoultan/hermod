// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package proto

import (
	"testing"

	"google.golang.org/protobuf/reflect/protodesc"
)

// TestDescriptorParses touches the embedded descriptor so a hand-edit of the
// generated code that breaks it fails here rather than at process start, and
// pins the names hermod-agent in the field depends on. See the same test in
// pkg/comm/source/grpc/proto for the incident that made it necessary.
func TestDescriptorParses(t *testing.T) {
	fd := (&ConnectRequest{}).ProtoReflect().Descriptor().ParentFile()

	if got, want := fd.Package(), "hermod.agent.v1"; string(got) != want {
		t.Errorf("proto package = %q, want %q", got, want)
	}
	if got := fd.Services().Len(); got != 1 {
		t.Fatalf("services = %d, want 1", got)
	}
	if got, want := fd.Services().Get(0).FullName(), "hermod.agent.v1.AgentService"; string(got) != want {
		t.Errorf("service = %q, want %q", got, want)
	}
	const wantGoPkg = "github.com/gsoultan/hermod/pkg/agent/proto"
	if got := protodesc.ToFileDescriptorProto(fd).GetOptions().GetGoPackage(); got != wantGoPkg {
		t.Errorf("go_package = %q, want %q", got, wantGoPkg)
	}
}
