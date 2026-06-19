package worker

import (
	"errors"
	"testing"

	"github.com/user/hermod/internal/storage"
)

// compile-time assertion that the adapter satisfies the full storage interface.
var _ storage.Storage = (*apiStorage)(nil)

func TestNewAPIStorage_SatisfiesStorage(t *testing.T) {
	client := NewWorkerAPIClient("http://localhost:0", "token")
	s := NewAPIStorage(client)
	if s == nil {
		t.Fatal("expected non-nil storage adapter")
	}
}

func TestAPIStorage_SafeDefaults(t *testing.T) {
	s := NewAPIStorage(NewWorkerAPIClient("http://localhost:0", "token"))
	ctx := t.Context()

	t.Run("InitAndPingNoop", func(t *testing.T) {
		if err := s.Init(ctx); err != nil {
			t.Errorf("Init() = %v; want nil", err)
		}
		if err := s.Ping(ctx); err != nil {
			t.Errorf("Ping() = %v; want nil", err)
		}
	})

	t.Run("GetNodeStatesEmpty", func(t *testing.T) {
		states, err := s.GetNodeStates(ctx, "wf1")
		if err != nil {
			t.Errorf("GetNodeStates() error = %v; want nil", err)
		}
		if states == nil {
			t.Error("GetNodeStates() returned nil map; want empty non-nil map")
		}
		if len(states) != 0 {
			t.Errorf("GetNodeStates() len = %d; want 0", len(states))
		}
	})

	t.Run("UnsupportedGettersReturnNotFound", func(t *testing.T) {
		if _, err := s.GetUser(ctx, "u1"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("GetUser() error = %v; want ErrNotFound", err)
		}
		if _, err := s.GetSetting(ctx, "key"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("GetSetting() error = %v; want ErrNotFound", err)
		}
		if _, err := s.GetVHost(ctx, "v1"); !errors.Is(err, storage.ErrNotFound) {
			t.Errorf("GetVHost() error = %v; want ErrNotFound", err)
		}
	})

	t.Run("UnsupportedMutationsNoError", func(t *testing.T) {
		if err := s.UpdateNodeState(ctx, "wf1", "n1", nil); err != nil {
			t.Errorf("UpdateNodeState() = %v; want nil", err)
		}
		if err := s.SaveSetting(ctx, "k", "v"); err != nil {
			t.Errorf("SaveSetting() = %v; want nil", err)
		}
	})
}
