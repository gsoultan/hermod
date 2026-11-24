// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/gsoultan/hermod/internal/engine/registry"
	infrahttp "github.com/gsoultan/hermod/internal/infra/transport/http"
	"github.com/gsoultan/hermod/internal/storage"
	agentpb "github.com/gsoultan/hermod/pkg/agent/proto"
)

// hermod-agent connects to the gRPC server the process really builds, with a
// token checked against the database first-time setup installs.
func TestAnAgentConnectsToTheServerTheProcessBuilds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HERMOD_CONFIG_DIR", dir)
	srv := NewServer(registry.NewRegistry(nil), nil, nil, filepath.Join(dir, "config.yaml"), nil)
	t.Cleanup(srv.Stop)

	lis := bufconn.Listen(1 << 20)
	grpcSrv := srv.newGRPCServer()
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	connect := func(token string) (*agentpb.ConnectResponse, error) {
		cctx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		st, err := agentpb.NewAgentServiceClient(conn).Connect(cctx)
		if err != nil {
			return nil, err
		}
		_ = st.Send(&agentpb.ConnectRequest{Body: &agentpb.ConnectRequest_Hello{Hello: &agentpb.Hello{}}})
		return st.Recv()
	}

	// Before setup there is no database to hold agents.
	if _, err := connect("hag_x_y"); status.Code(err) != codes.Unavailable {
		t.Fatalf("before setup: err = %v, want Unavailable", err)
	}

	body, _ := json.Marshal(map[string]any{
		"db": map[string]string{"type": "sqlite", "conn": filepath.Join(dir, "hermod.db"),
			"crypto_master_key": "0123456789abcdef0123456789abcdef"},
		"admin": map[string]string{"username": "admin", "password": "admin-password", "email": "admin@example.com"},
	})
	rec := httptest.NewRecorder()
	infrahttp.NewInfraHandler(srv.Handler).FinalizeInitialSetup(rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/setup", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup returned %d: %s", rec.Code, rec.Body)
	}

	token, hash, err := storage.NewAgentToken("agt-setup")
	if err != nil {
		t.Fatal(err)
	}
	as, ok := srv.Handler.Storage.(storage.AgentStore)
	if !ok {
		t.Fatal("the store setup installs cannot hold agents")
	}
	if err := as.CreateAgent(ctx, storage.Agent{ID: "agt-setup", VHost: "default", Name: "pi", TokenHash: hash}); err != nil {
		t.Fatal(err)
	}
	if _, err := connect("hag_agt-setup_wrong"); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong token: err = %v, want Unauthenticated", err)
	}
	first, err := connect(token)
	if err != nil || first.GetWelcome().GetAgentId() != "agt-setup" {
		t.Fatalf("connect = %v, %v; want a Welcome", first, err)
	}
}
