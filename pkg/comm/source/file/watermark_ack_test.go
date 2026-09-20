package file

// The file source recorded a file as consumed when it was dequeued.
//
// pop() advanced lastMTime to the file's modification time the moment the file
// came off the queue, before a single one of its rows had been delivered, and
// GetState persisted that. One CSV file can be thousands of rows, so an
// interrupted run skipped the remainder of that file — and, because the
// watermark is a timestamp rather than a position, every other file sharing
// its modification time as well.
//
// This is the last of the sources that had the watermark-on-read bug. It was
// left out of the first sweep because the fix is not the mechanical one the
// others took: a file yields many rows, and a streaming reader cannot know a
// row is the last until it asks for one more. So the unit that gets
// acknowledged here is the file, not the row.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/writer"
)

// csvDir writes CSV files, each with a distinct modification time so the
// watermark has something to order by.
func csvDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	i := 0
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		mt := base.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatalf("chtimes %s: %v", name, err)
		}
		i++
	}
	return dir
}

func csvSource(t *testing.T, dir string) *GenericFileSource {
	t.Helper()
	s := NewGenericFileSource(GenericConfig{
		Backend:   BackendLocal,
		Format:    FormatCSV,
		LocalPath: dir,
		Pattern:   "*.csv",
	})
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func readN(t *testing.T, s *GenericFileSource, n int) []hermod.Message {
	t.Helper()
	out := make([]hermod.Message, 0, n)
	for len(out) < n {
		msg, err := s.Read(context.Background())
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if msg == nil {
			t.Fatalf("source ran dry after %d messages, wanted %d", len(out), n)
		}
		out = append(out, msg)
	}
	return out
}

// The core of it: rows read but not acknowledged must not move the stored
// watermark, or a restart skips them.
func TestWatermarkDoesNotAdvanceForUnacknowledgedRows(t *testing.T) {
	dir := csvDir(t, map[string]string{
		"a.csv": "id,name\n1,one\n2,two\n3,three\n",
	})
	s := csvSource(t, dir)

	readN(t, s, 3) // the whole file, nothing acknowledged

	if got := s.GetState()["last_mtime_unix"]; got != "" {
		t.Errorf("watermark = %q with every row unacknowledged; a restart here skips the file", got)
	}
}

// Acknowledging some of a file's rows is not enough: the watermark is a
// timestamp, so moving it past this file skips whatever is left of it.
func TestWatermarkNeedsEveryRowOfTheFile(t *testing.T) {
	dir := csvDir(t, map[string]string{
		"a.csv": "id,name\n1,one\n2,two\n3,three\n",
	})
	s := csvSource(t, dir)
	ctx := context.Background()

	msgs := readN(t, s, 3)
	for _, m := range msgs[:2] {
		if err := s.Ack(ctx, m); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	if got := s.GetState()["last_mtime_unix"]; got != "" {
		t.Errorf("watermark = %q with one row of the file still in flight", got)
	}

	if err := s.Ack(ctx, msgs[2]); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	// Still nothing: the reader has not been told it reached the end of the
	// file yet, and until it has, row three might not be the last row.
	if got := s.GetState()["last_mtime_unix"]; got != "" {
		t.Errorf("watermark = %q before the file was known to be fully read", got)
	}

	drainToEndOfFile(t, s)
	if got := s.GetState()["last_mtime_unix"]; got == "" {
		t.Error("watermark did not advance once the file was fully read and every row acknowledged")
	}
}

// drainToEndOfFile asks for one more row, which is how the reader finds out it
// has reached the end of the file: a streaming reader cannot know row N was the
// last until row N+1 is asked for. In a running pipeline this is just the next
// Read; here it has to be explicit.
func drainToEndOfFile(t *testing.T, s *GenericFileSource) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = s.Read(ctx)
}

// Files are processed oldest first and the watermark is monotonic, so a later
// file completing while an earlier one is still in flight must not move it —
// that would skip the earlier file entirely.
func TestWatermarkDoesNotSkipAnEarlierFileStillInFlight(t *testing.T) {
	dir := csvDir(t, map[string]string{
		"a.csv": "id\n1\n2\n",
		"b.csv": "id\n3\n",
	})
	s := csvSource(t, dir)
	ctx := context.Background()

	msgs := readN(t, s, 3) // two rows of a.csv, then b.csv's single row

	// Acknowledge only the last file's row.
	if err := s.Ack(ctx, msgs[2]); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := s.GetState()["last_mtime_unix"]; got != "" {
		t.Errorf("watermark = %q while the earlier file is still in flight: "+
			"a restart would skip it, because the watermark is a timestamp", got)
	}

	for _, m := range msgs[:2] {
		_ = s.Ack(ctx, m)
	}
	if got := s.GetState()["last_mtime_unix"]; got == "" {
		t.Error("watermark did not advance once both files were fully acknowledged")
	}
}

// Resuming must pick up from the acknowledged watermark.
func TestSetStateRestoresTheAcknowledgedWatermark(t *testing.T) {
	dir := csvDir(t, map[string]string{"a.csv": "id\n1\n"})
	s := csvSource(t, dir)

	want := strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)
	s.SetState(map[string]string{"last_mtime_unix": want})

	if got := s.GetState()["last_mtime_unix"]; got != want {
		t.Errorf("watermark = %q after SetState, want %q", got, want)
	}
}

// writeParquetAt is writeParquet with a caller-chosen directory and file name,
// so two files can sit in the same scan with different modification times.
func writeParquetAt(t *testing.T, dir, name, schema string, rows ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)

	fw, err := local.NewLocalFileWriter(path)
	if err != nil {
		t.Fatalf("local writer: %v", err)
	}
	pw, err := writer.NewJSONWriter(schema, fw, 1)
	if err != nil {
		t.Fatalf("parquet writer: %v", err)
	}
	for _, r := range rows {
		if err := pw.Write(r); err != nil {
			t.Fatalf("write row %s: %v", r, err)
		}
	}
	if err := pw.WriteStop(); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	if err := fw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

func setMTime(t *testing.T, path string, mt time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// A row's identity is not unique across files, so it cannot be what the
// accounting is keyed on.
//
// With key_field set — the normal configuration — a parquet row's message ID is
// the key column's value. Two daily exports of the same table therefore hand
// out the same IDs, and a single map from message ID to file silently
// reassigns the first file's rows to the second. The first file then never
// completes, and because the mark only advances across an acknowledged prefix,
// the watermark freezes: every restart re-reads from the same point, forever.
func TestRowsWithTheSameIdInDifferentFilesAreNotConfused(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	first := writeParquetAt(t, dir, "day1.parquet", pqPlainSchema, `{"id":"k1","qty":1}`)
	second := writeParquetAt(t, dir, "day2.parquet", pqPlainSchema, `{"id":"k1","qty":2}`)
	setMTime(t, first, base)
	setMTime(t, second, base.Add(time.Minute))

	s := parquetSource(t, dir, GenericConfig{KeyField: "id"})
	ctx := context.Background()

	m1, err := s.Read(ctx)
	if err != nil || m1 == nil {
		t.Fatalf("reading the first file: %v", err)
	}
	m2, err := s.Read(ctx)
	if err != nil || m2 == nil {
		t.Fatalf("reading the second file: %v", err)
	}
	if m1.ID() != m2.ID() {
		t.Fatalf("precondition: IDs %q and %q differ, so this test proves nothing", m1.ID(), m2.ID())
	}

	// The later file first: its own file is complete, but the earlier one is
	// still in flight, so nothing may move yet.
	if err := s.Ack(ctx, m2); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := s.GetState()["last_mtime_unix"]; got != "" {
		t.Errorf("watermark = %q while the earlier file is still in flight", got)
	}

	if err := s.Ack(ctx, m1); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := s.GetState()["last_mtime_unix"]; got == "" {
		t.Error("watermark never advanced though both files were fully acknowledged: " +
			"the second file's row took the first file's place in the accounting, " +
			"so the first can never complete and the source re-reads from here forever")
	}
}

// Resuming from a timestamp alone skips every file that shares it.
//
// The scan keeps files whose modification time is strictly after the
// watermark, so once one file of a batch is acknowledged, its siblings are
// filtered out on the next start. A batch drop lands files in the same second,
// and filesystems with one-second mtime granularity make "the same second"
// literally the same value — so this is the ordinary case for a directory
// written all at once, not an edge of it. Nothing errors; the files are simply
// never read.
func TestResumingDoesNotSkipFilesSharingAModificationTime(t *testing.T) {
	dir := t.TempDir()
	mt := time.Now().Add(-time.Hour).Truncate(time.Second)
	for _, name := range []string{"a.csv", "b.csv", "c.csv"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("id\n"+name+"\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		setMTime(t, p, mt)
	}

	first := csvSource(t, dir)
	ctx := context.Background()

	// Consume a.csv completely: one row, acknowledged, and the reader told it
	// reached the end.
	msg, err := first.Read(ctx)
	if err != nil || msg == nil {
		t.Fatalf("reading the first file: %v", err)
	}
	if err := first.Ack(ctx, msg); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	drainToEndOfFile(t, first)

	state := first.GetState()
	if state["last_mtime_unix"] == "" {
		t.Fatal("precondition: nothing was persisted, so there is no resume to test")
	}

	// A restart. b.csv and c.csv have never been read.
	second := csvSource(t, dir)
	second.SetState(state)

	seen := map[string]bool{}
	for range 4 {
		m, err := second.Read(ctx)
		if err != nil {
			t.Fatalf("Read after resume: %v", err)
		}
		if m == nil {
			break
		}
		if v, ok := m.Data()["id"].(string); ok {
			seen[v] = true
		}
	}

	for _, want := range []string{"b.csv", "c.csv"} {
		if !seen[want] {
			t.Errorf("%s was never read after resuming; it shares a modification time with the "+
				"acknowledged file, and the scan keeps only what is strictly newer", want)
		}
	}
}
