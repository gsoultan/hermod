package sql

import (
	"errors"
	"testing"

	"github.com/gsoultan/hermod/internal/storage"
)

func mlScriptStore(t *testing.T) storage.MLScriptStore {
	t.Helper()
	withKey(t, "ml-script-test-key")
	var st storage.Storage = newRotationStorage(t)
	ss, ok := st.(storage.MLScriptStore)
	if !ok {
		t.Fatal("the SQL store does not implement MLScriptStore")
	}
	return ss
}

const scriptV1 = "def train(df, spec):\n    return 1\n\ndef export_onnx(model, spec):\n    return b''\n"
const scriptV2 = scriptV1 + "# tuned\n"

func TestMLScriptsAreVersionedBySHA256(t *testing.T) {
	st, ctx := mlScriptStore(t), t.Context()

	first, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: scriptV1, Description: "first", CreatedBy: "ada"})
	if err != nil {
		t.Fatalf("PutMLScript: %v", err)
	}
	if first.Version != 1 || first.SHA256 != storage.MLScriptSHA256(scriptV1) || first.CreatedAt.IsZero() {
		t.Errorf("first = %+v", first)
	}

	same, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: scriptV1, CreatedBy: "bob"})
	if err != nil {
		t.Fatalf("PutMLScript (same source): %v", err)
	}
	if same.Version != 1 || same.CreatedBy != "ada" {
		t.Errorf("saving the same source again made %+v, want version 1 unchanged", same)
	}

	second, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: scriptV2, CreatedBy: "bob"})
	if err != nil {
		t.Fatalf("PutMLScript (new source): %v", err)
	}
	if second.Version != 2 || second.SHA256 != storage.MLScriptSHA256(scriptV2) {
		t.Errorf("second = %+v", second)
	}

	// Going back to the first source is a new version, not a rewrite of history.
	third, err := st.PutMLScript(ctx, storage.MLScript{VHost: "tenant-a", Name: "trees", Source: scriptV1, CreatedBy: "cy"})
	if err != nil || third.Version != 3 || third.SHA256 != first.SHA256 {
		t.Errorf("third = %+v, %v", third, err)
	}

	got, err := st.GetMLScript(ctx, "tenant-a", "trees")
	if err != nil {
		t.Fatalf("GetMLScript: %v", err)
	}
	if got.Version != 3 || got.Source != scriptV1 || got.CreatedBy != "cy" {
		t.Errorf("latest = %+v", got)
	}

	versions, err := st.ListMLScriptVersions(ctx, "tenant-a", "trees")
	if err != nil {
		t.Fatalf("ListMLScriptVersions: %v", err)
	}
	if len(versions) != 3 || versions[0].Version != 3 || versions[2].Version != 1 || versions[2].Description != "first" {
		t.Errorf("versions = %+v", versions)
	}
	for _, v := range versions {
		if v.Source != "" {
			t.Errorf("a version listing carried the source of version %d", v.Version)
		}
	}
}

func TestMLScriptsStayInTheirVHostAndListLatest(t *testing.T) {
	st, ctx := mlScriptStore(t), t.Context()
	for _, s := range []storage.MLScript{
		{VHost: "a", Name: "zeta", Source: scriptV1},
		{VHost: "a", Name: "alpha", Source: scriptV1},
		{VHost: "a", Name: "alpha", Source: scriptV2},
		{VHost: "b", Name: "alpha", Source: scriptV1},
	} {
		if _, err := st.PutMLScript(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.ListMLScripts(ctx, "a")
	if err != nil {
		t.Fatalf("ListMLScripts: %v", err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || list[0].Version != 2 || list[1].Name != "zeta" || list[0].Source != "" {
		t.Errorf("list = %+v", list)
	}
	if _, err := st.GetMLScript(ctx, "c", "alpha"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("another vhost's script: err = %v, want ErrNotFound", err)
	}

	if err := st.DeleteMLScript(ctx, "a", "alpha"); err != nil {
		t.Fatalf("DeleteMLScript: %v", err)
	}
	if err := st.DeleteMLScript(ctx, "a", "alpha"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("second delete: err = %v, want ErrNotFound", err)
	}
	if vs, _ := st.ListMLScriptVersions(ctx, "a", "alpha"); len(vs) != 0 {
		t.Errorf("deleting a script left %d versions", len(vs))
	}
	if got, err := st.GetMLScript(ctx, "b", "alpha"); err != nil || got.Version != 1 {
		t.Errorf("vhost b's script = %+v, %v", got, err)
	}
	if err := st.DeleteMLScripts(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListMLScripts(ctx, "b"); len(list) != 0 {
		t.Errorf("vhost b still holds %d scripts", len(list))
	}
}

func TestPutMLScriptRefusesAnInvalidScript(t *testing.T) {
	st := mlScriptStore(t)
	for name, s := range map[string]storage.MLScript{
		"no vhost":    {Name: "trees", Source: scriptV1},
		"path name":   {VHost: "v", Name: "../etc", Source: scriptV1},
		"empty":       {VHost: "v", Name: "trees", Source: "  \n"},
		"too large":   {VHost: "v", Name: "trees", Source: string(make([]byte, storage.MaxMLScriptBytes+1))},
		"description": {VHost: "v", Name: "trees", Source: scriptV1, Description: string(make([]byte, 1025))},
	} {
		if _, err := st.PutMLScript(t.Context(), s); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
}

// A deleted vhost must not leave its scripts for a later vhost of the same name.
func TestDeletingAVHostDeletesItsScripts(t *testing.T) {
	withKey(t, "ml-script-test-key")
	s := newRotationStorage(t)
	if err := s.CreateVHost(t.Context(), storage.VHost{ID: "vh-1", Name: "tenant-a"}); err != nil {
		t.Fatalf("CreateVHost: %v", err)
	}
	for _, vhost := range []string{"tenant-a", "tenant-b"} {
		if _, err := s.PutMLScript(t.Context(), storage.MLScript{VHost: vhost, Name: "trees", Source: scriptV1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteVHost(t.Context(), "vh-1"); err != nil {
		t.Fatalf("DeleteVHost: %v", err)
	}
	if list, _ := s.ListMLScripts(t.Context(), "tenant-a"); len(list) != 0 {
		t.Errorf("tenant-a was deleted and still holds %d scripts", len(list))
	}
	if list, _ := s.ListMLScripts(t.Context(), "tenant-b"); len(list) != 1 {
		t.Errorf("deleting tenant-a left tenant-b with %d scripts, want 1", len(list))
	}
}
