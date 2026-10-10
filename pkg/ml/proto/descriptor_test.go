package proto

import (
	"testing"

	"google.golang.org/protobuf/reflect/protodesc"
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
