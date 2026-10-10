//go:build integration
// +build integration

package mongodb

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

// The same contract the SQL store keeps (internal/storage/sql/ml_scripts_test.go),
// against a live MongoDB.
func TestMongoMLScripts(t *testing.T) {
	s, _ := newTraceMongo(t)
	st, ok := s.(storage.MLScriptStore)
	if !ok {
		t.Fatal("the MongoDB store does not implement storage.MLScriptStore")
	}
	ctx := t.Context()
	v1, v2 := "def train(df, spec):\n    return 1\n", "def train(df, spec):\n    return 2\n"

	first, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: v1, CreatedBy: "ada"})
	if err != nil || first.Version != 1 || first.SHA256 != storage.MLScriptSHA256(v1) {
		t.Fatalf("PutMLScript = %+v, %v", first, err)
	}
	if same, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: v1}); err != nil || same.Version != 1 {
		t.Errorf("same source again = %+v, %v; want version 1", same, err)
	}
	if second, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: v2}); err != nil || second.Version != 2 {
		t.Errorf("new source = %+v, %v; want version 2", second, err)
	}
	got, err := st.GetMLScript(ctx, "tenant-a", "trees")
	if err != nil || got.Version != 2 || got.Source != v2 {
		t.Errorf("GetMLScript = %+v, %v", got, err)
	}
	if vs, _ := st.ListMLScriptVersions(ctx, "tenant-a", "trees"); len(vs) != 2 || vs[0].Version != 2 || vs[0].Source != "" {
		t.Errorf("versions = %+v", vs)
	}
	if list, _ := st.ListMLScripts(ctx, "tenant-a"); len(list) != 1 || list[0].Version != 2 {
		t.Errorf("list = %+v", list)
	}
	if _, err := st.GetMLScript(ctx, "tenant-b", "trees"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost read it: %v", err)
	}
	if err := st.DeleteMLScript(ctx, "tenant-a", "trees"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteMLScript(ctx, "tenant-a", "trees"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}
