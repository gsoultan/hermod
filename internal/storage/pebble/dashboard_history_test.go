package pebble

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/internal/storage"
)

// Pebble does not store dashboard history, and saying so honestly is right —
// a recorder that returned nil would leave the caller believing it has a
// series it cannot read back.
//
// But "honest" and "loud" are different things. The registry samples every
// five seconds on every node, so a bare error here is 17,280 logged failures a
// day, forever, on a deployment that stores no history at all: the backend
// with the smallest data footprint produces the largest log one. The caller
// can only stop asking if it can tell "this backend will never support this"
// apart from "this write failed", and that distinction is what
// hermod.ErrNotSupported exists to carry.
func TestDashboardHistoryIsUnsupportedNotFailing(t *testing.T) {
	s, err := NewPebbleStorage(t.TempDir())
	if err != nil {
		t.Fatalf("failed to open pebble storage: %v", err)
	}
	if c, ok := s.(interface{ Close() error }); ok {
		t.Cleanup(func() { _ = c.Close() })
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"RecordDashboardSample", func() error {
			return s.RecordDashboardSample(t.Context(), storage.DashboardSample{})
		}},
		{"PurgeDashboardHistory", func() error {
			return s.PurgeDashboardHistory(t.Context(), storage.DashboardSample{}.Timestamp)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("reported success without storing anything, so the caller " +
					"believes it has history it cannot read back")
			}
			if !errors.Is(err, hermod.ErrNotSupported) {
				t.Errorf("returned %v, which is indistinguishable from a transient "+
					"write failure; the sampler cannot stop retrying a backend that "+
					"will never support this, and logs it every five seconds forever", err)
			}
		})
	}
}
