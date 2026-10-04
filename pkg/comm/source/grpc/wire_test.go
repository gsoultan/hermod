package grpcsource

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/comm/source/grpc/proto"
)

// The gRPC ingress, over a real connection.
//
// The registry handover semantics are covered next door; what nothing covered
// is the wire itself — a Publish arriving through the real gRPC stack and
// coming out of Read — and the two ways the ingress could betray its caller:
// an API-key check that silently stops checking, and an anonymous record that
// keeps an empty ID all the way to a sink's primary key.

// wireServer starts the real Server on an in-process listener and returns a
// connected client.
func wireServer(t *testing.T, st storage.Storage) proto.SourceServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterSourceServiceServer(srv, &Server{Storage: st})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing the in-process server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return proto.NewSourceServiceClient(conn)
}

// A record published through the real stack comes out of Read intact.
func TestARecordSurvivesTheWire(t *testing.T) {
	src := NewGrpcSource("/grpc/wire")
	t.Cleanup(func() { _ = src.Close() })
	client := wireServer(t, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	resp, err := client.Publish(ctx, &proto.PublishRequest{
		Path:      "/grpc/wire",
		Id:        "rec-1",
		Operation: "create",
		Table:     "orders",
		Schema:    "sales",
		Payload:   []byte(`{"total":42}`),
		Metadata:  map[string]string{"origin": "wire-test"},
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if resp.Id != "rec-1" {
		t.Errorf("the response names id %q, want rec-1", resp.Id)
	}

	msg, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if msg.ID() != "rec-1" || msg.Table() != "orders" || msg.Schema() != "sales" {
		t.Errorf("the record arrived as id=%q table=%q schema=%q", msg.ID(), msg.Table(), msg.Schema())
	}
	if msg.Operation() != hermod.OpCreate {
		t.Errorf("operation arrived as %q, want %q", msg.Operation(), hermod.OpCreate)
	}
	if string(msg.Payload()) != `{"total":42}` {
		t.Errorf("payload arrived as %q", msg.Payload())
	}
	if msg.Metadata()["origin"] != "wire-test" {
		t.Errorf("metadata arrived as %v", msg.Metadata())
	}
}

// failingStorage cannot list sources. Only the method the API-key check uses
// is implemented; the embedded nil interface panics on anything else, which is
// the point — this path must touch nothing more.
type failingStorage struct{ storage.Storage }

func (failingStorage) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return nil, 0, errors.New("the sources table is unavailable")
}

// keyedStorage says the path requires an API key.
type keyedStorage struct{ storage.Storage }

func (keyedStorage) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return []storage.Source{{
		Type:   "grpc",
		Config: map[string]string{"path": "/grpc/keyed", "api_key": "sesame"},
	}}, 1, nil
}

// When the storage that holds the API keys cannot be read, the ingress must
// refuse the publish, not skip the check. It used to fail open: any error from
// ListSources silently disabled authentication, so a storage hiccup turned a
// keyed endpoint into an anonymous one — and accepted the write.
func TestAStorageErrorDoesNotDisableTheAPIKeyCheck(t *testing.T) {
	src := NewGrpcSource("/grpc/failing")
	t.Cleanup(func() { _ = src.Close() })
	client := wireServer(t, failingStorage{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := client.Publish(ctx, &proto.PublishRequest{
		Path:    "/grpc/failing",
		Id:      "rec-1",
		Payload: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("a publish was accepted while the API keys were unreadable")
	}
	if !strings.Contains(err.Error(), "api key") {
		t.Errorf("the publish failed, but not because the key check failed closed: %v", err)
	}
}

// The wrong key is refused and the right key is accepted, through the real
// metadata path.
func TestTheAPIKeyIsActuallyChecked(t *testing.T) {
	src := NewGrpcSource("/grpc/keyed")
	t.Cleanup(func() { _ = src.Close() })
	client := wireServer(t, keyedStorage{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req := &proto.PublishRequest{Path: "/grpc/keyed", Id: "rec-1", Payload: []byte(`{}`)}

	if _, err := client.Publish(ctx, req); err == nil {
		t.Error("a publish with no key at all was accepted on a keyed path")
	}
	wrong := metadata.AppendToOutgoingContext(ctx, "x-api-key", "guess")
	if _, err := client.Publish(wrong, req); err == nil {
		t.Error("a publish with the wrong key was accepted")
	}
	right := metadata.AppendToOutgoingContext(ctx, "x-api-key", "sesame")
	if _, err := client.Publish(right, req); err != nil {
		t.Errorf("the correct key was refused: %v", err)
	}
	if msg, err := src.Read(ctx); err != nil || msg.ID() != "rec-1" {
		t.Errorf("the authorised record did not come through: msg=%v err=%v", msg, err)
	}
}

// A publish without an ID must be given one. The handler's comment promised
// "ID will be generated if missing" and nothing generated it, so every
// anonymous record reached the sinks with an empty ID — and at a SQL sink the
// empty string is a primary key, so they all upserted the same row, each
// overwriting the last.
func TestAnAnonymousPublishGetsAnID(t *testing.T) {
	src := NewGrpcSource("/grpc/anon")
	t.Cleanup(func() { _ = src.Close() })
	client := wireServer(t, nil)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	ids := make(map[string]bool)
	for i := range 2 {
		resp, err := client.Publish(ctx, &proto.PublishRequest{
			Path:    "/grpc/anon",
			Payload: []byte(`{}`),
		})
		if err != nil {
			t.Fatalf("publish %d: %v", i+1, err)
		}
		msg, err := src.Read(ctx)
		if err != nil {
			t.Fatalf("read %d: %v", i+1, err)
		}
		if msg.ID() == "" {
			t.Fatal("an anonymous record went through with an empty ID; at a SQL " +
				"sink that is a primary key, and every such record upserts the same row")
		}
		if resp.Id != msg.ID() {
			t.Errorf("the caller was told id %q but the record carries %q", resp.Id, msg.ID())
		}
		if ids[msg.ID()] {
			t.Fatalf("two anonymous records share the ID %q", msg.ID())
		}
		ids[msg.ID()] = true
	}
}

// A producer that describes its record as a row image — `after`, and no
// `payload` — must not lose the row. The handler stored `after` and then stored
// `payload` unconditionally; on the message those are one field, so the empty
// payload erased the image it had just been given. The call still answered
// "dispatched", and the sink received an id, an operation and a table with no
// row in it.
func TestARecordSentAsAfterKeepsItsRow(t *testing.T) {
	cases := []struct {
		name string
		req  *proto.PublishRequest
		want string
	}{
		{
			name: "after only",
			req:  &proto.PublishRequest{After: []byte(`{"order_id":2}`)},
			want: `{"order_id":2}`,
		},
		{
			// Both set has always meant the payload; that must not change for
			// the producers already sending it.
			name: "payload and after",
			req:  &proto.PublishRequest{Payload: []byte(`{"order_id":3}`), After: []byte(`{"stale":true}`)},
			want: `{"order_id":3}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const path = "/grpc/after"
			src := NewGrpcSource(path)
			t.Cleanup(func() { _ = src.Close() })
			client := wireServer(t, nil)

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			tc.req.Path = path
			tc.req.Operation = "create"
			tc.req.Table = "orders"
			if _, err := client.Publish(ctx, tc.req); err != nil {
				t.Fatalf("publish: %v", err)
			}
			msg, err := src.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got := string(msg.Payload()); got != tc.want {
				t.Errorf("the row arrived as %q, want %q", got, tc.want)
			}
		})
	}
}

// sourcesStorage holds exactly the sources it is given.
type sourcesStorage struct {
	storage.Storage
	sources []storage.Source
}

func (s sourcesStorage) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	return s.sources, len(s.sources), nil
}

// A path that is receiving but that the key store holds no source for must be
// refused. "No source for this path" used to mean "no key required", so the
// check failed open whenever the store and the running workflows disagreed —
// which is what a database switch produces: the workflow keeps running, the new
// database has never heard of its source, and a keyed path took publishes with
// no key at all.
func TestAPathTheKeyStoreDoesNotKnowIsRefused(t *testing.T) {
	src := NewGrpcSource("/grpc/unstored")
	t.Cleanup(func() { _ = src.Close() })
	client := wireServer(t, keyedStorage{})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err := client.Publish(ctx, &proto.PublishRequest{Path: "/grpc/unstored", Payload: []byte(`{}`)})
	if err == nil {
		t.Fatal("a publish was accepted on a path the key store has no source for")
	}
	if !strings.Contains(err.Error(), "/grpc/unstored") {
		t.Errorf("the refusal does not name the path: %v", err)
	}
}

// A source saved without a path listens on the default one, so its key must be
// found under the default one. Compared as stored, the empty path matched
// nothing and the source's key was never asked for.
func TestASourceWithNoPathIsKeyedUnderTheDefaultPath(t *testing.T) {
	src := NewGrpcSource("")
	t.Cleanup(func() { _ = src.Close() })
	client := wireServer(t, sourcesStorage{sources: []storage.Source{{
		Type:   "grpc",
		Config: map[string]string{"api_key": "sesame"},
	}}})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req := &proto.PublishRequest{Payload: []byte(`{}`)}
	if _, err := client.Publish(ctx, req); err == nil {
		t.Error("a publish with no key was accepted on a keyed source that has no path")
	}
	right := metadata.AppendToOutgoingContext(ctx, "x-api-key", "sesame")
	if _, err := client.Publish(right, req); err != nil {
		t.Errorf("the correct key was refused: %v", err)
	}
}

// swappableStorage stands in for the handler's storage, which is nil on a
// first run and replaced when setup or a database switch installs another.
type swappableStorage struct{ current storage.Storage }

func (s *swappableStorage) get() storage.Storage { return s.current }

// The key check must use the store that is current when the publish arrives,
// not the one that existed when the server was built. The store is hot-swapped
// by first-time setup and by a database switch, and an ingress holding the old
// one either skips the check (it was nil) or goes on reading keys from a
// database the install has moved off.
func TestTheKeyCheckUsesTheStoreCurrentAtPublish(t *testing.T) {
	src := NewGrpcSource("/grpc/keyed")
	t.Cleanup(func() { _ = src.Close() })

	holder := &swappableStorage{}
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	proto.RegisterSourceServiceServer(srv, &Server{StorageFunc: holder.get})
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

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

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// The store arrives after the server is already serving.
	holder.current = keyedStorage{}

	req := &proto.PublishRequest{Path: "/grpc/keyed", Id: "rec-1", Payload: []byte(`{}`)}
	if _, err := client.Publish(ctx, req); err == nil {
		t.Error("a publish with no key was accepted after a keyed store was installed")
	}
	right := metadata.AppendToOutgoingContext(ctx, "x-api-key", "sesame")
	if _, err := client.Publish(right, req); err != nil {
		t.Errorf("the correct key was refused: %v", err)
	}
}
