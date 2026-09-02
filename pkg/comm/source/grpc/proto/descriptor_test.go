package proto

import (
	"testing"

	"google.golang.org/protobuf/reflect/protodesc"
)

// The generated descriptor is a length-prefixed binary blob embedded in a Go
// string literal. `go build` and `go vet` both compile it happily whatever it
// contains, because nothing decodes it until package init runs — so a
// hand-edit of this file (a module rename, a find-and-replace over generated
// code) can change a string without changing the varint that says how long it
// is, and the first sign of trouble is a panic on process start.
//
// That happened: renaming the module path rewrote the go_package string from
// 49 to 53 bytes and left its length prefix at 49, and every build gate passed
// while the binary died at init with "slice bounds out of range [-1:]". This
// package had no tests, so `go test ./...` reported "[no test files]" and
// checked nothing.
//
// Touching the descriptor at all is enough to catch that class of damage,
// because init has to parse the whole blob before any of these calls return.
func TestDescriptorParses(t *testing.T) {
	fd := (&PublishRequest{}).ProtoReflect().Descriptor().ParentFile()

	// The wire contract external clients call. Renaming the Go package must
	// never move this; if it does, it is a break for every existing client.
	if got, want := fd.Package(), "hermod.source.grpc.v1"; string(got) != want {
		t.Errorf("proto package = %q, want %q", got, want)
	}
	if got := fd.Services().Len(); got != 1 {
		t.Fatalf("services = %d, want 1", got)
	}
	if got, want := fd.Services().Get(0).FullName(), "hermod.source.grpc.v1.SourceService"; string(got) != want {
		t.Errorf("service = %q, want %q", got, want)
	}

	// go_package must match the module path, or importers of the generated
	// code and the descriptor disagree about where this package lives.
	const wantGoPkg = "github.com/gsoultan/Hermod/pkg/comm/source/grpc/proto"
	if got := protodesc.ToFileDescriptorProto(fd).GetOptions().GetGoPackage(); got != wantGoPkg {
		t.Errorf("go_package = %q, want %q", got, wantGoPkg)
	}
}
