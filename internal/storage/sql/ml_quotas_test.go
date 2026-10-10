package sql

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func mlQuotaStore(t *testing.T) (storage.MLQuotaStore, *sqlStorage) {
	t.Helper()
	s := newRotationStorage(t)
	var st storage.Storage = s
	qs, ok := st.(storage.MLQuotaStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLQuotaStore")
	}
	return qs, s
}

func TestMLQuotasRoundTripPerVHost(t *testing.T) {
	st, _ := mlQuotaStore(t)
	ctx := t.Context()

	if _, err := st.GetMLQuotas(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("a vhost with no quotas: err = %v, want ErrNotFound", err)
	}

	q := storage.MLQuotas{VHost: "tenant-a", MaxModels: new(int64(3)), MaxPredictionsPerSecond: new(12.5), UpdatedBy: "root"}
	if err := st.PutMLQuotas(ctx, q); err != nil {
		t.Fatalf("PutMLQuotas: %v", err)
	}
	got, err := st.GetMLQuotas(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("GetMLQuotas: %v", err)
	}
	if got.MaxModels == nil || *got.MaxModels != 3 || got.MaxPredictionsPerSecond == nil || *got.MaxPredictionsPerSecond != 12.5 {
		t.Errorf("round trip = %+v", got)
	}
	if got.MaxDatasets != nil || got.MaxDatasetRows != nil || got.MaxDatasetBytes != nil || got.MaxConcurrentTrainings != nil {
		t.Errorf("unset quotas came back set: %+v", got)
	}
	if got.VHost != "tenant-a" || got.UpdatedBy != "root" || got.UpdatedAt.IsZero() {
		t.Errorf("bookkeeping = %q %q %v", got.VHost, got.UpdatedBy, got.UpdatedAt)
	}
	if _, err := st.GetMLQuotas(ctx, "tenant-b"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("tenant-b read tenant-a's quotas: err = %v", err)
	}

	// A second put replaces the whole set: a quota left out is unset again.
	if err := st.PutMLQuotas(ctx, storage.MLQuotas{VHost: "tenant-a", MaxDatasets: new(int64(0))}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetMLQuotas(ctx, "tenant-a")
	if got.MaxModels != nil || got.MaxDatasets == nil || *got.MaxDatasets != 0 {
		t.Errorf("after replacing: %+v", got)
	}

	if err := st.DeleteMLQuotas(ctx, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetMLQuotas(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestPutMLQuotasRefusesANegativeQuota(t *testing.T) {
	st, _ := mlQuotaStore(t)
	if err := st.PutMLQuotas(t.Context(), storage.MLQuotas{VHost: "v", MaxModels: new(int64(-1))}); err == nil {
		t.Fatal("a negative quota was saved")
	}
}

// A deleted vhost must not leave its quotas for a later vhost of the same name.
func TestDeletingAVHostDeletesItsQuotas(t *testing.T) {
	st, s := mlQuotaStore(t)
	ctx := t.Context()
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"tenant-a", "tenant-b"} {
		if err := st.PutMLQuotas(ctx, storage.MLQuotas{VHost: v, MaxModels: new(int64(1))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteVHost(ctx, "vh-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetMLQuotas(ctx, "tenant-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("tenant-a was deleted and kept its quotas: err = %v", err)
	}
	if _, err := st.GetMLQuotas(ctx, "tenant-b"); err != nil {
		t.Errorf("deleting tenant-a removed tenant-b's quotas: %v", err)
	}
}
