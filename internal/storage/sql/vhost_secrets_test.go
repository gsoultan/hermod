package sql

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// A vhost keeps its own secrets: saved from the UI, encrypted at rest, and read
// only by that vhost's workflows. Before this there was nowhere in Hermod to
// save one -- a new API key meant changing the server's environment.

func vhostSecretStore(t *testing.T) (*sqlStorage, storage.VHostSecretStore) {
	t.Helper()
	withKey(t, "vhost-secret-test-key")
	s := newRotationStorage(t)
	return s, s
}

func putSecret(t *testing.T, st storage.VHostSecretStore, vhost, name, value, by string) {
	t.Helper()
	if err := st.PutVHostSecret(t.Context(), storage.VHostSecret{VHost: vhost, Name: name, Value: value, UpdatedBy: by}); err != nil {
		t.Fatalf("PutVHostSecret(%s/%s): %v", vhost, name, err)
	}
}

func TestVHostSecretRoundTripsAndStaysInItsVHost(t *testing.T) {
	_, st := vhostSecretStore(t)
	putSecret(t, st, "tenant-a", "API_KEY", "a-key", "ada")
	putSecret(t, st, "tenant-b", "API_KEY", "b-key", "bob")

	for vhost, want := range map[string]string{"tenant-a": "a-key", "tenant-b": "b-key"} {
		got, err := st.GetVHostSecret(t.Context(), vhost, "API_KEY")
		if err != nil {
			t.Fatalf("GetVHostSecret(%s): %v", vhost, err)
		}
		if got.Value != want {
			t.Errorf("%s/API_KEY = %q, want %q", vhost, got.Value, want)
		}
	}

	if _, err := st.GetVHostSecret(t.Context(), "tenant-a", "MISSING"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a secret the vhost does not hold: err = %v, want ErrNotFound", err)
	}
	if _, err := st.GetVHostSecret(t.Context(), "tenant-c", "API_KEY"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a vhost with no secrets: err = %v, want ErrNotFound", err)
	}
}

// The list is what the UI and the editor's picker show: names, and who changed
// them when. A value never leaves storage through it.
func TestVHostSecretListNamesAndNeverValues(t *testing.T) {
	_, st := vhostSecretStore(t)
	putSecret(t, st, "tenant-a", "ZETA", "z-value", "ada")
	putSecret(t, st, "tenant-a", "ALPHA", "a-value", "bob")
	putSecret(t, st, "tenant-b", "OTHER", "o-value", "bob")

	list, err := st.ListVHostSecrets(t.Context(), "tenant-a")
	if err != nil {
		t.Fatalf("ListVHostSecrets: %v", err)
	}
	if len(list) != 2 || list[0].Name != "ALPHA" || list[1].Name != "ZETA" {
		t.Fatalf("list = %+v, want ALPHA then ZETA and nothing of tenant-b", list)
	}
	for _, s := range list {
		if s.Value != "" {
			t.Errorf("the list carries %s's value", s.Name)
		}
		if s.UpdatedAt.IsZero() || s.CreatedAt.IsZero() {
			t.Errorf("%s has no timestamps: %+v", s.Name, s)
		}
	}
	if list[0].UpdatedBy != "bob" {
		t.Errorf("ALPHA updated_by = %q, want bob", list[0].UpdatedBy)
	}
}

func TestVHostSecretRotateReplacesTheValue(t *testing.T) {
	_, st := vhostSecretStore(t)
	putSecret(t, st, "tenant-a", "API_KEY", "old", "ada")
	first, _ := st.GetVHostSecret(t.Context(), "tenant-a", "API_KEY")

	putSecret(t, st, "tenant-a", "API_KEY", "new", "bob")
	got, err := st.GetVHostSecret(t.Context(), "tenant-a", "API_KEY")
	if err != nil {
		t.Fatalf("GetVHostSecret: %v", err)
	}
	if got.Value != "new" || got.UpdatedBy != "bob" {
		t.Errorf("after rotate = %q by %q, want new by bob", got.Value, got.UpdatedBy)
	}
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("rotate moved created_at from %v to %v", first.CreatedAt, got.CreatedAt)
	}
	list, _ := st.ListVHostSecrets(t.Context(), "tenant-a")
	if len(list) != 1 {
		t.Errorf("rotate left %d rows, want 1", len(list))
	}
}

func TestVHostSecretDelete(t *testing.T) {
	_, st := vhostSecretStore(t)
	putSecret(t, st, "tenant-a", "API_KEY", "a-key", "ada")
	putSecret(t, st, "tenant-b", "API_KEY", "b-key", "ada")

	if err := st.DeleteVHostSecret(t.Context(), "tenant-a", "API_KEY"); err != nil {
		t.Fatalf("DeleteVHostSecret: %v", err)
	}
	if _, err := st.GetVHostSecret(t.Context(), "tenant-a", "API_KEY"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
	if got, err := st.GetVHostSecret(t.Context(), "tenant-b", "API_KEY"); err != nil || got.Value != "b-key" {
		t.Errorf("deleting tenant-a's secret touched tenant-b's: %q, %v", got.Value, err)
	}
	if err := st.DeleteVHostSecret(t.Context(), "tenant-a", "API_KEY"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("deleting what is not there: err = %v, want ErrNotFound", err)
	}
}

// A deleted vhost must not leave its secrets behind for a later vhost of the
// same name to inherit.
func TestDeletingAVHostDeletesItsSecrets(t *testing.T) {
	s, st := vhostSecretStore(t)
	if err := s.CreateVHost(t.Context(), storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	putSecret(t, st, "tenant-a", "API_KEY", "a-key", "ada")
	putSecret(t, st, "tenant-b", "API_KEY", "b-key", "ada")

	if err := s.DeleteVHost(t.Context(), "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if list, _ := st.ListVHostSecrets(t.Context(), "tenant-a"); len(list) != 0 {
		t.Errorf("tenant-a was deleted and still holds %d secrets", len(list))
	}
	if list, _ := st.ListVHostSecrets(t.Context(), "tenant-b"); len(list) != 1 {
		t.Errorf("deleting tenant-a left tenant-b with %d secrets, want 1", len(list))
	}
}

func TestVHostSecretIsEncryptedAtRest(t *testing.T) {
	s, st := vhostSecretStore(t)
	putSecret(t, st, "tenant-a", "API_KEY", "plain-text-value", "ada")

	var stored string
	if err := s.queryRow(t.Context(), "SELECT value FROM vhost_secrets WHERE vhost = ? AND name = ?", "tenant-a", "API_KEY").Scan(&stored); err != nil {
		t.Fatalf("reading the stored column: %v", err)
	}
	if stored == "" || strings.Contains(stored, "plain-text-value") {
		t.Errorf("the stored value is %q: not encrypted", stored)
	}
}

// Rotating the master key re-encrypts connector configs. A secret left under
// the old key would be unreadable the moment the new key is installed.
func TestVHostSecretsSurviveAMasterKeyRotation(t *testing.T) {
	s, st := vhostSecretStore(t)
	putSecret(t, st, "tenant-a", "API_KEY", "a-key", "ada")

	if err := s.ReEncryptSecrets(t.Context(), "the-next-key"); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}
	crypto.SetMasterKey("the-next-key")

	got, err := st.GetVHostSecret(t.Context(), "tenant-a", "API_KEY")
	if err != nil {
		t.Fatalf("after rotation GetVHostSecret: %v", err)
	}
	if got.Value != "a-key" {
		t.Errorf("after rotation = %q, want a-key", got.Value)
	}
}
