package worker

import (
	"errors"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
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

// A worker's registry runs the same five-second dashboard sampler as the
// control plane, but this adapter holds no database — the samples belong to
// the node that owns one. Returning nil made every tick look like a successful
// write, so the sampler kept calling across the network forever; returning a
// bare error would have filled worker logs at the same rate. Wrapping
// hermod.ErrNotSupported lets the sampler latch after one tick and stop.
func TestDashboardHistoryOnAWorkerIsUnsupportedNotSilent(t *testing.T) {
	s := NewAPIStorage(&WorkerAPIClient{})

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"RecordDashboardSample", func() error {
			return s.RecordDashboardSample(t.Context(), storage.DashboardSample{})
		}},
		{"PurgeDashboardHistory", func() error {
			return s.PurgeDashboardHistory(t.Context(), time.Now())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, hermod.ErrNotSupported) {
				t.Errorf("returned %v; the sampler cannot tell that this node will "+
					"never store history, so it keeps asking every five seconds", err)
			}
		})
	}
}
