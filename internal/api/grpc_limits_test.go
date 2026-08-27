package api

import (
	"testing"
	"time"
)

// The gRPC server's limits.
//
// grpc.NewServer() was called with no options at all. The port is EXPOSEd by
// the Dockerfile and the only authentication is a per-path API key checked
// inside Publish, so everything before that check is reachable
// unauthenticated — and gRPC's default MaxConcurrentStreams is *unlimited*,
// so one client could open streams until the process ran out of memory.
//
// These assert the constants the server is built from, which is the honest
// scope for them: whether grpc-go applies an option it was given is grpc-go's
// contract, not something a test here can meaningfully re-prove. What this
// catches is somebody removing a limit, or setting one to a value that would
// break the endpoint it protects.
func TestConcurrentStreamsAreBounded(t *testing.T) {
	if maxConcurrentStreams <= 0 {
		t.Fatal("MaxConcurrentStreams is unset; gRPC's default is unlimited, so one " +
			"client can open streams until the process runs out of memory")
	}
	// A ceiling nobody legitimate meets, not a tuning target. If this ever
	// drops to something a real producer could hit, that is a throughput
	// regression wearing a security hat.
	if maxConcurrentStreams < 100 {
		t.Errorf("maxConcurrentStreams=%d is low enough that a normal producer could "+
			"meet it; this is meant to stop exhaustion, not shape traffic", maxConcurrentStreams)
	}
}

func TestIdleConnectionsAreReclaimedButStreamsAreNot(t *testing.T) {
	if grpcMaxConnectionIdle <= 0 {
		t.Error("MaxConnectionIdle is unset; connections doing nothing are never reclaimed")
	}
	// Long enough that a producer pausing between batches is not disconnected.
	if grpcMaxConnectionIdle < time.Minute {
		t.Errorf("grpcMaxConnectionIdle=%v would disconnect a producer that pauses "+
			"between batches", grpcMaxConnectionIdle)
	}
}

// The keepalive floor guards against a ping flood. It must be generous enough
// that an ordinary client is never the thing it catches.
func TestTheKeepaliveFloorIsAFloodGuardNotATuningKnob(t *testing.T) {
	if grpcMinKeepaliveInterval <= 0 {
		t.Fatal("no keepalive enforcement; a client may ping as fast as it likes, " +
			"which costs the server work per ping and the client nothing")
	}
	if grpcMinKeepaliveInterval > time.Minute {
		t.Errorf("grpcMinKeepaliveInterval=%v is strict enough to disconnect "+
			"well-behaved clients, which is how a limit like this gets removed "+
			"entirely later", grpcMinKeepaliveInterval)
	}
	// The server's own probe must not violate its own floor, or it would be
	// enforcing a rule it breaks.
	if grpcKeepaliveTime < grpcMinKeepaliveInterval {
		t.Errorf("the server probes every %v but requires clients to wait %v; "+
			"the policy contradicts itself", grpcKeepaliveTime, grpcMinKeepaliveInterval)
	}
}

func TestReceiveSizeIsBounded(t *testing.T) {
	if maxRecvMsgSize <= 0 {
		t.Error("MaxRecvMsgSize is unset")
	}
}
