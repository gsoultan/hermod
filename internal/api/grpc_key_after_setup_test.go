package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/gsoultan/hermod/internal/engine/registry"
	infrahttp "github.com/gsoultan/hermod/internal/infra/transport/http"
	"github.com/gsoultan/hermod/internal/storage"
	grpcsource "github.com/gsoultan/hermod/pkg/comm/source/grpc"
	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
)

// The gRPC ingress on an install that has just been set up.
//
// A first run has no database, so main.go builds the API server with nil
// storage and starts the gRPC listener straight away. The ingress copied that
// nil when the listener started, and a nil store is the one case in which
// Publish skips the API-key check. First-time setup then opened the database
// and handed it to the handler, the registry and the worker — but not to the
// ingress, which went on holding nil.
//
// So on every fresh install, until somebody restarted the process, a gRPC
// source with an API key accepted publishes carrying no key or the wrong one.
// The key check has unit tests and they pass: they hand the ingress a store
// directly, which is the one thing the running system did not do.
//
// This starts where the operator starts — a server with no database, then the
// setup request the wizard sends — and serves the server the process really
// builds.
func TestTheGRPCKeyCheckSeesTheStorageSetupInstalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)

	// The server as main.go builds it on a first run: no storage.
	srv := NewServer(registry.NewRegistry(nil), nil, nil, filepath.Join(dir, "config.yaml"), nil)
	t.Cleanup(srv.Stop)

	lis := bufconn.Listen(1 << 20)
	grpcSrv := srv.newGRPCServer()
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing the in-process server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := proto.NewSourceServiceClient(conn)

	// First-time setup, through the handler the wizard calls.
	body, err := json.Marshal(map[string]any{
		"db": map[string]string{
			"type":              "sqlite",
			"conn":              filepath.Join(dir, "hermod.db"),
			"crypto_master_key": "0123456789abcdef0123456789abcdef",
		},
		"admin": map[string]string{
			"username": "admin",
			"password": "admin-password",
			"email":    "admin@example.com",
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	rec := httptest.NewRecorder()
	infrahttp.NewInfraHandler(srv.Handler).FinalizeInitialSetup(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/setup", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup returned %d, body: %s", rec.Code, rec.Body.String())
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// The operator creates a gRPC source and puts a key on it.
	const path = "/grpc/after-setup"
	if err := srv.Handler.Storage.CreateSource(ctx, storage.Source{
		ID:     "grpc-after-setup",
		Name:   "orders",
		Type:   "grpc",
		VHost:  "/",
		Active: true,
		Config: map[string]string{"path": path, "api_key": "sesame"},
	}); err != nil {
		t.Fatalf("creating the keyed source: %v", err)
	}

	// What starting its workflow does: the engine builds the source, which
	// takes the path.
	src := grpcsource.NewGrpcSource(path)
	t.Cleanup(func() { _ = src.Close() })

	req := &proto.PublishRequest{Path: path, Id: "rec-1", Payload: []byte(`{}`)}

	if _, err := client.Publish(ctx, req); err == nil {
		t.Error("a publish with no key was accepted on a keyed source: the ingress " +
			"is still checking against the storage it was started with, not the one setup installed")
	}
	wrong := metadata.AppendToOutgoingContext(ctx, "x-api-key", "guess")
	if _, err := client.Publish(wrong, req); err == nil {
		t.Error("a publish with the wrong key was accepted on a keyed source")
	}
	right := metadata.AppendToOutgoingContext(ctx, "x-api-key", "sesame")
	if _, err := client.Publish(right, req); err != nil {
		t.Errorf("the correct key was refused: %v", err)
	}
}
