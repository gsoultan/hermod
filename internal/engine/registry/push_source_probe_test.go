package registry

import (
	"context"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/factory"
	"github.com/gsoultan/hermod/pkg/comm/message"
	sourceform "github.com/gsoultan/hermod/pkg/comm/source/form"
	sourcegraphql "github.com/gsoultan/hermod/pkg/comm/source/graphql"
	grpcsource "github.com/gsoultan/hermod/pkg/comm/source/grpc"
	"github.com/gsoultan/hermod/pkg/comm/source/webhook"
)

// Test Connection on a source that is receiving.
//
// A push source — gRPC, webhook, GraphQL, form — has nothing to connect to. It
// holds a path in an in-process registry and whatever arrives for that path is
// handed to it. Testing its connection builds a second source from the same
// configuration, pings it and closes it, and building one took the path:
// the newest registration owned it, by design, so that a worker taking a
// workflow over receives instead of the one it replaces. The probe was the
// newest, and when it closed it was the owner, so it deleted the path.
//
// The running workflow went on reporting itself as running. It was reading a
// channel nothing dispatched to any more, and every request for the path was
// refused as unregistered until someone restarted the workflow. Pressing Test
// Connection on a working source was enough.
//
// This goes the way the button goes — Registry.TestSource, the discovery
// service, the registry's own source construction — against a source built the
// way the engine builds it.
func TestTestingAConnectionLeavesTheRunningSourceItsPath(t *testing.T) {
	cases := []struct {
		sourceType string
		path       string
		dispatch   func(path string, msg hermod.Message) error
	}{
		{"grpc", "/grpc/probed", grpcsource.Dispatch},
		{"webhook", "/api/webhooks/probed", webhook.Dispatch},
		{"graphql", "/api/graphql/probed", sourcegraphql.Dispatch},
		{"form", "/api/forms/probed", sourceform.Dispatch},
	}
	for _, tc := range cases {
		t.Run(tc.sourceType, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			reg := NewRegistry(nil)
			cfg := factory.SourceConfig{Type: tc.sourceType, Config: map[string]string{"path": tc.path}}

			// The running workflow's source.
			running, err := reg.CreateSource(ctx, cfg)
			if err != nil {
				t.Fatalf("building the running source: %v", err)
			}
			t.Cleanup(func() { _ = running.Close() })

			// The operator presses Test Connection.
			if err := reg.TestSource(ctx, cfg); err != nil {
				t.Fatalf("the connection test failed: %v", err)
			}

			msg := message.AcquireMessage()
			msg.SetID("after-the-probe")
			if err := tc.dispatch(tc.path, msg); err != nil {
				message.ReleaseMessage(msg)
				t.Fatalf("a request after the connection test was refused: %v; the probe "+
					"took the path from the running source and removed it on closing", err)
			}

			got, err := running.Read(ctx)
			if err != nil {
				t.Fatalf("the running source did not receive the request: %v", err)
			}
			if got.ID() != "after-the-probe" {
				t.Errorf("the running source received %q", got.ID())
			}
		})
	}
}
