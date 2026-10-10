package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/comm/transformer"
	"github.com/gsoultan/hermod/pkg/comm/transformer/lookup"
)

// Startup bounds reference_lookup to the upload storage's directory as
// config.yaml configures it: a file the upload endpoint stored there is read,
// a file elsewhere on the machine is not.
func TestStartupBoundsReferenceLookupToTheUploadDirectory(t *testing.T) {
	t.Cleanup(func() { lookup.SetReferenceRoots() })
	dir := t.TempDir()
	uploads := filepath.Join(dir, "files")
	if err := os.MkdirAll(uploads, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("file_storage:\n  type: local\n  local_dir: files\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(uploads, "countries.csv")
	outside := filepath.Join(t.TempDir(), "countries.csv")
	for _, p := range []string{inside, outside} {
		if err := os.WriteFile(p, []byte("code,name\nFR,France\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	setupRegistry(nil, nil, nil, &Options{configPath: cfgPath})

	tf, ok := transformer.Get("reference_lookup")
	if !ok {
		t.Fatal("reference_lookup is not linked into the binary")
	}
	lookupIn := func(path string) error {
		msg := message.AcquireMessage()
		defer message.ReleaseMessage(msg)
		msg.SetData("c", "FR")
		_, err := tf.Transform(t.Context(), msg, map[string]any{"filePath": path, "keyColumn": "code", "keyField": "c"})
		return err
	}
	if err := lookupIn(inside); err != nil {
		t.Errorf("a file in the configured upload directory: %v", err)
	}
	if err := lookupIn(outside); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("a file outside it: err = %v, want it refused", err)
	}
}
