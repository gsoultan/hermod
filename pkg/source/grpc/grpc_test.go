package grpcsource

import (
	"context"
	"testing"

	"github.com/user/hermod/internal/storage"
	"github.com/user/hermod/pkg/source/grpc/proto"
	"google.golang.org/grpc/metadata"
)

type mockStorage struct {
	storage.Storage
	sources []storage.Source
}

func (m *mockStorage) ListSources(ctx context.Context, filter storage.CommonFilter) ([]storage.Source, int, error) {
	return m.sources, len(m.sources), nil
}

func TestPublishAuthentication(t *testing.T) {
	ms := &mockStorage{
		sources: []storage.Source{
			{
				Type: "grpc",
				Config: map[string]string{
					"path":    "/test/path",
					"api_key": "secret-token",
				},
			},
		},
	}

	server := &Server{Storage: ms}

	// Register the source to avoid "no gRPC source registered" error
	_ = Register("/test/path")
	defer Unregister("/test/path")

	tests := []struct {
		name    string
		token   string
		path    string
		wantErr bool
	}{
		{
			name:    "Valid Token",
			token:   "secret-token",
			path:    "/test/path",
			wantErr: false,
		},
		{
			name:    "Invalid Token",
			token:   "wrong-token",
			path:    "/test/path",
			wantErr: true,
		},
		{
			name:    "Missing Token",
			token:   "",
			path:    "/test/path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.token != "" {
				ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("x-api-key", tt.token))
			}

			req := &proto.PublishRequest{
				Path:    tt.path,
				Payload: []byte("hello"),
			}

			_, err := server.Publish(ctx, req)
			if (err != nil) != tt.wantErr {
				t.Errorf("Publish() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
