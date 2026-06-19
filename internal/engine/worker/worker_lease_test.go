package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/user/hermod/internal/testutil"
)

// leaseMockStorage lets each test control the lease method behavior.
type leaseMockStorage struct {
	testutil.BaseMockStorage
	renewOK   bool
	renewErr  error
	acquireOK bool
	acquanErr error
}

func (m *leaseMockStorage) RenewWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return m.renewOK, m.renewErr
}

func (m *leaseMockStorage) AcquireWorkflowLease(ctx context.Context, workflowID, ownerID string, ttlSeconds int) (bool, error) {
	return m.acquireOK, m.acquanErr
}

func TestRenewLeaseOnce(t *testing.T) {
	tests := []struct {
		name  string
		store *leaseMockStorage
		want  leaseRenewalOutcome
	}{
		{"renewed", &leaseMockStorage{renewOK: true}, leaseHeld},
		{"renew transient error keeps lease", &leaseMockStorage{renewErr: errors.New("timeout")}, leaseTransientError},
		{"reacquired in place", &leaseMockStorage{renewOK: false, acquireOK: true}, leaseHeld},
		{"acquire transient error keeps lease", &leaseMockStorage{renewOK: false, acquanErr: errors.New("timeout")}, leaseTransientError},
		{"genuinely lost", &leaseMockStorage{renewOK: false, acquireOK: false}, leaseLost},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorker(tc.store, nil)
			w.SetWorkerConfig(0, 1, "guid-1", "")
			if got := w.renewLeaseOnce(t.Context(), "wf1"); got != tc.want {
				t.Errorf("renewLeaseOnce() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkerAPIClient_LeaseAlwaysOwned(t *testing.T) {
	c := NewWorkerAPIClient("http://localhost:0", "token")
	ctx := t.Context()

	if ok, err := c.AcquireWorkflowLease(ctx, "wf1", "owner", 30); err != nil || !ok {
		t.Errorf("AcquireWorkflowLease() = (%v, %v); want (true, nil)", ok, err)
	}
	if ok, err := c.RenewWorkflowLease(ctx, "wf1", "owner", 30); err != nil || !ok {
		t.Errorf("RenewWorkflowLease() = (%v, %v); want (true, nil)", ok, err)
	}
	if err := c.ReleaseWorkflowLease(ctx, "wf1", "owner"); err != nil {
		t.Errorf("ReleaseWorkflowLease() = %v; want nil", err)
	}
}

func TestJSONEqual(t *testing.T) {
	tests := []struct {
		name string
		a    any
		b    any
		want bool
	}{
		{"identical maps", map[string]any{"a": 1, "b": 2}, map[string]any{"a": 1, "b": 2}, true},
		{"map key order irrelevant", map[string]any{"b": 2, "a": 1}, map[string]any{"a": 1, "b": 2}, true},
		{"int vs float same value", map[string]any{"n": 1}, map[string]any{"n": 1.0}, true},
		{"different values", map[string]any{"n": 1}, map[string]any{"n": 2}, false},
		{"nil vs empty map differ", map[string]string(nil), map[string]string{}, false},
		{"equal string slices", []string{"x", "y"}, []string{"x", "y"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("jsonEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
