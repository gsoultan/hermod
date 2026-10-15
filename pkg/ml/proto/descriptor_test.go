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
	"google.golang.org/protobuf/reflect/protoreflect"
)

// The same guard pkg/comm/source/grpc/proto keeps: parsing the embedded
// descriptor at all catches a hand-edit that broke its length prefixes, and
// the names checked here are the wire contract applications call.
func TestDescriptorParses(t *testing.T) {
	fd := (&PredictRequest{}).ProtoReflect().Descriptor().ParentFile()

	if got, want := fd.Package(), "hermod.ml.v1"; string(got) != want {
		t.Errorf("proto package = %q, want %q", got, want)
	}
	if got := fd.Services().Len(); got != 1 {
		t.Fatalf("services = %d, want 1", got)
	}
	if got, want := fd.Services().Get(0).FullName(), "hermod.ml.v1.InferenceService"; string(got) != want {
		t.Errorf("service = %q, want %q", got, want)
	}
	const wantGoPkg = "github.com/gsoultan/hermod/pkg/ml/proto"
	if got := protodesc.ToFileDescriptorProto(fd).GetOptions().GetGoPackage(); got != wantGoPkg {
		t.Errorf("go_package = %q, want %q", got, wantGoPkg)
	}
}

// The job RPCs are the async twin of Predict and Recommend; WatchJob streams
// a job's progress until it ends.
func TestJobRPCs(t *testing.T) {
	svc := (&PredictRequest{}).ProtoReflect().Descriptor().ParentFile().Services().Get(0)
	for _, rpc := range []struct {
		name   string
		stream bool
	}{{"Predict", false}, {"Recommend", false}, {"SubmitJob", false}, {"GetJob", false}, {"CancelJob", false}, {"WatchJob", true}} {
		m := svc.Methods().ByName(protoreflect.Name(rpc.name))
		if m == nil {
			t.Errorf("InferenceService has no %s", rpc.name)
			continue
		}
		if m.IsStreamingServer() != rpc.stream || m.IsStreamingClient() {
			t.Errorf("%s streams the server %v, the client %v; want %v, false", rpc.name, m.IsStreamingServer(), m.IsStreamingClient(), rpc.stream)
		}
	}
}
