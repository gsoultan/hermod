//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// The same contract the SQL store keeps (internal/storage/sql/vhost_secrets_test.go),
// against a live MongoDB: a vhost's secrets round-trip, stay in their vhost,
// list without values, are ciphertext at rest, go with their vhost, and survive
// a master-key rotation.
func TestMongoVHostSecrets(t *testing.T) {
	s, db := newTraceMongo(t)
	st, ok := s.(storage.VHostSecretStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.VHostSecretStore")
	}
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	crypto.SetMasterKey("vhost-secret-test-key")
	ctx := t.Context()

	put := func(vhost, name, value, by string) {
		t.Helper()
		if err := st.PutVHostSecret(ctx, storage.VHostSecret{VHost: vhost, Name: name, Value: value, UpdatedBy: by}); err != nil {
			t.Fatalf("PutVHostSecret(%s/%s): %v", vhost, name, err)
		}
	}
	put("tenant-a", "ZETA", "z-value", "ada")
	put("tenant-a", "API_KEY", "old", "ada")
	put("tenant-b", "API_KEY", "b-key", "bob")
	put("tenant-a", "API_KEY", "a-key", "bob") // rotate

	got, err := st.GetVHostSecret(ctx, "tenant-a", "API_KEY")
	if err != nil || got.Value != "a-key" || got.UpdatedBy != "bob" {
		t.Fatalf("tenant-a/API_KEY = %q by %q, %v; want a-key by bob", got.Value, got.UpdatedBy, err)
	}
	if other, err := st.GetVHostSecret(ctx, "tenant-b", "API_KEY"); err != nil || other.Value != "b-key" {
		t.Errorf("tenant-b/API_KEY = %q, %v; want b-key", other.Value, err)
	}
	if _, err := st.GetVHostSecret(ctx, "tenant-a", "MISSING"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("a missing secret: err = %v, want ErrNotFound", err)
	}

	list, err := st.ListVHostSecrets(ctx, "tenant-a")
	if err != nil || len(list) != 2 || list[0].Name != "API_KEY" || list[1].Name != "ZETA" {
		t.Fatalf("list = %+v, %v; want API_KEY then ZETA", list, err)
	}
	for _, sec := range list {
		if sec.Value != "" {
			t.Errorf("the list carries %s's value", sec.Name)
		}
	}

	var raw struct {
		Value string `bson:"value"`
	}
	if err := db.Collection(vhostSecretsCollection).FindOne(ctx, bson.M{"_id": "tenant-a/API_KEY"}).Decode(&raw); err != nil {
		t.Fatalf("reading the stored document: %v", err)
	}
	if raw.Value == "" || strings.Contains(raw.Value, "a-key") {
		t.Errorf("the stored value is %q: not encrypted", raw.Value)
	}

	if err := s.(*mongoStorage).ReEncryptSecrets(ctx, "the-next-key"); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}
	crypto.SetMasterKey("the-next-key")
	if got, err := st.GetVHostSecret(ctx, "tenant-a", "API_KEY"); err != nil || got.Value != "a-key" {
		t.Errorf("after a key rotation = %q, %v; want a-key", got.Value, err)
	}

	if err := st.DeleteVHostSecret(ctx, "tenant-a", "ZETA"); err != nil {
		t.Fatalf("DeleteVHostSecret: %v", err)
	}
	if err := st.DeleteVHostSecret(ctx, "tenant-a", "ZETA"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("deleting what is not there: err = %v, want ErrNotFound", err)
	}

	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	if err := s.DeleteVHost(ctx, "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if list, _ := st.ListVHostSecrets(ctx, "tenant-a"); len(list) != 0 {
		t.Errorf("tenant-a was deleted and still holds %d secrets", len(list))
	}
	if list, _ := st.ListVHostSecrets(ctx, "tenant-b"); len(list) != 1 {
		t.Errorf("deleting tenant-a left tenant-b with %d secrets, want 1", len(list))
	}
}
