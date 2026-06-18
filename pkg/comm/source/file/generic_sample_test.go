package file

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenericFileSourceSampleCSV verifies that sampling a CSV-backed generic
// file source returns the first record and does not consume real data (the
// subsequent Read must still return the same data).
func TestGenericFileSourceSampleCSV(t *testing.T) {
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "data.csv")
	content := "id,name,age\n1,John,30\n2,Jane,25\n"
	if err := os.WriteFile(csvPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	src := NewGenericFileSource(GenericConfig{
		Backend:   BackendLocal,
		Format:    FormatCSV,
		LocalPath: dir,
		Pattern:   "*.csv",
	})
	defer src.Close()

	ctx := t.Context()

	msg, err := src.Sample(ctx, "")
	if err != nil {
		t.Fatalf("Sample failed: %v", err)
	}
	if msg == nil {
		t.Fatal("expected sample message, got nil")
	}
	if got := msg.Data()["id"]; got != "1" {
		t.Errorf("expected id=1, got %v", got)
	}

	// Sampling must be non-destructive: a real Read still yields the first row.
	readMsg, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("Read after Sample failed: %v", err)
	}
	if readMsg == nil {
		t.Fatal("expected Read to return data after Sample (non-destructive)")
	}
	if got := readMsg.Data()["id"]; got != "1" {
		t.Errorf("expected Read id=1 after Sample, got %v", got)
	}
}

// TestGenericFileSourceSampleRaw verifies raw-file sampling returns file bytes.
func TestGenericFileSourceSampleRaw(t *testing.T) {
	dir := t.TempDir()
	payload := []byte("hello world")
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	src := NewGenericFileSource(GenericConfig{
		Backend:   BackendLocal,
		Format:    FormatRaw,
		LocalPath: dir,
	})
	defer src.Close()

	msg, err := src.Sample(t.Context(), "")
	if err != nil {
		t.Fatalf("Sample failed: %v", err)
	}
	if msg == nil {
		t.Fatal("expected sample message, got nil")
	}
	// For raw files the bytes are carried in the payload (After).
	if got := string(msg.After()); got != string(payload) {
		t.Errorf("expected payload %q, got %q", payload, got)
	}
	if got := msg.Metadata()["source"]; got != "file" {
		t.Errorf("expected source=file metadata, got %v", got)
	}
}

// TestGenericFileSourceSampleNoFiles ensures sampling an empty directory returns
// a clear error instead of panicking.
func TestGenericFileSourceSampleNoFiles(t *testing.T) {
	src := NewGenericFileSource(GenericConfig{
		Backend:   BackendLocal,
		Format:    FormatRaw,
		LocalPath: t.TempDir(),
	})
	defer src.Close()

	if _, err := src.Sample(t.Context(), ""); err == nil {
		t.Fatal("expected error when sampling an empty directory")
	}
}

// TestCSVSourceSampleCustomNoPanic ensures sampling a custom (reader-backed)
// CSV source fails gracefully instead of dereferencing a nil pointer.
func TestCSVSourceSampleCustomNoPanic(t *testing.T) {
	src := NewCSVSourceFromReadCloser(nil, ',', true)
	if _, err := src.Sample(t.Context(), ""); err == nil {
		t.Fatal("expected error sampling a custom CSV source, got nil")
	}
}
