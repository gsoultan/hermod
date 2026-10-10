package mldataset

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/ml/worker"
)

// fakeWorker keeps what each dataset was sent, as the hermod-ml worker does.
type fakeWorker struct {
	mu      sync.Mutex
	rows    map[string][]map[string]any // "vhost/dataset"
	appends int
	replace bool
	fail    error
	pinged  bool
}

func newFakeWorker() *fakeWorker { return &fakeWorker{rows: map[string][]map[string]any{}} }

func (f *fakeWorker) AppendRows(_ context.Context, vhost, dataset string, rows []map[string]any, replace bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	f.appends++
	f.replace = f.replace || replace
	key := vhost + "/" + dataset
	f.rows[key] = append(f.rows[key], rows...)
	return len(f.rows[key]), nil
}

func (f *fakeWorker) Dataset(_ context.Context, vhost, dataset string) (worker.DatasetInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows, ok := f.rows[vhost+"/"+dataset]
	if !ok {
		return worker.DatasetInfo{}, fmt.Errorf("%w: no dataset", worker.ErrNotFound)
	}
	return worker.DatasetInfo{Name: dataset, Rows: len(rows)}, nil
}

func (f *fakeWorker) Ready(context.Context) error {
	f.pinged = true
	return nil
}

func record(vhost string, data map[string]any) hermod.Message {
	m := message.AcquireMessage()
	m.SetOperation(hermod.OpCreate)
	for k, v := range data {
		m.SetData(k, v)
	}
	if vhost != "" {
		m.SetVHost(vhost)
	}
	return m
}

func newSink(t *testing.T, w Appender, vhost string, cfg map[string]string) *Sink {
	t.Helper()
	s, err := New(cfg, vhost, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestAWholeRecordIsAppendedFlat(t *testing.T) {
	w := newFakeWorker()
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers"})
	msg := record("tenant-a", map[string]any{"age": 30, "plan": "pro", "address": map[string]any{"city": "Jakarta", "geo": map[string]any{"lat": 1.5}}})
	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := w.rows["tenant-a/customers"]
	if len(got) != 1 {
		t.Fatalf("rows = %+v", w.rows)
	}
	row := got[0]
	if row["age"] != 30 || row["plan"] != "pro" || row["address_city"] != "Jakarta" || row["address_geo_lat"] != 1.5 {
		t.Errorf("row = %v, want the record flattened", row)
	}
	if _, nested := row["address"]; nested {
		t.Errorf("the nested object was kept as well: %v", row)
	}
	if w.replace {
		t.Error("the sink replaced the dataset; it only ever appends")
	}
}

func TestColumnsPickAndRenameFields(t *testing.T) {
	w := newFakeWorker()
	s := newSink(t, w, "tenant-a", map[string]string{
		"dataset":         "customers",
		"column_mappings": `[{"source_field":"customer.age","target_column":"age"},{"source_field":"","target_column":"churned"},{"source_field":"missing","target_column":"tenure"}]`,
	})
	msg := record("tenant-a", map[string]any{"customer": map[string]any{"age": 41}, "churned": "yes", "secret": "x"})
	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	row := w.rows["tenant-a/customers"][0]
	if len(row) != 3 || row["age"] != 41.0 || row["churned"] != "yes" || row["tenure"] != nil {
		t.Errorf("row = %v, want exactly age, churned and an empty tenure", row)
	}
}

func TestMaskedColumnsAreMaskedAndTheMessageIsNot(t *testing.T) {
	w := newFakeWorker()
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers", "mask_fields": "email, phone", "mask_type": "email"})
	msg := record("tenant-a", map[string]any{"email": "ada@example.com", "phone": 62812345, "age": 30})
	if err := s.Write(t.Context(), msg); err != nil {
		t.Fatal(err)
	}
	row := w.rows["tenant-a/customers"][0]
	if row["email"] != "a****@example.com" || row["phone"] != "****" || row["age"] != 30 {
		t.Errorf("row = %v", row)
	}
	if msg.Data()["email"] != "ada@example.com" {
		t.Errorf("the sink masked the message itself: %v", msg.Data())
	}
}

func TestABatchIsOneAppend(t *testing.T) {
	w := newFakeWorker()
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers"})
	var batch []hermod.Message
	for i := range 5 {
		batch = append(batch, record("tenant-a", map[string]any{"i": i}))
	}
	del := record("tenant-a", map[string]any{"i": 99})
	del.(*message.DefaultMessage).SetOperation(hermod.OpDelete)
	batch = append(batch, del)
	if err := s.WriteBatch(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if w.appends != 1 || len(w.rows["tenant-a/customers"]) != 5 {
		t.Errorf("appends = %d, rows = %d; want one append of the five records that are not deletes", w.appends, len(w.rows["tenant-a/customers"]))
	}
}

// A workflow writes only its own vhost's datasets: the vhost is the one the
// engine marked the message with, never something the payload says.
func TestADatasetIsTheWorkflowsOwnVHosts(t *testing.T) {
	w := newFakeWorker()
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers"})

	err := s.Write(t.Context(), record("tenant-b", map[string]any{"vhost": "tenant-a", "x": 1}))
	if !errors.Is(err, hermod.ErrPermanent) {
		t.Fatalf("a tenant-b workflow wrote through tenant-a's sink: err = %v", err)
	}
	if len(w.rows) != 0 {
		t.Fatalf("rows reached a dataset: %v", w.rows)
	}

	if err := s.Write(t.Context(), record("", map[string]any{"x": 1})); err != nil {
		t.Fatal(err)
	}
	unscoped := newSink(t, w, "", map[string]string{"dataset": "customers"})
	if err := unscoped.Write(t.Context(), record("tenant-c", map[string]any{"x": 2})); err != nil {
		t.Fatal(err)
	}
	if err := unscoped.Write(t.Context(), record("", map[string]any{"x": 3})); err != nil {
		t.Fatal(err)
	}
	if len(w.rows["tenant-a/customers"]) != 1 || len(w.rows["tenant-c/customers"]) != 1 || len(w.rows["default/customers"]) != 1 {
		t.Errorf("rows = %v, want one each in tenant-a, tenant-c and default", w.rows)
	}
}

func TestTheDatasetStopsGrowingAtItsCap(t *testing.T) {
	w := newFakeWorker()
	for range 8 {
		w.rows["tenant-a/customers"] = append(w.rows["tenant-a/customers"], map[string]any{"old": true})
	}
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers", "max_rows": "10"})
	var batch []hermod.Message
	for i := range 5 {
		batch = append(batch, record("tenant-a", map[string]any{"i": i}))
	}
	if err := s.WriteBatch(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if n := len(w.rows["tenant-a/customers"]); n != 10 {
		t.Fatalf("rows = %d, want the cap of 10", n)
	}
	if err := s.Write(t.Context(), record("tenant-a", map[string]any{"i": 6})); err != nil {
		t.Fatalf("a record past the cap failed the workflow: %v", err)
	}
	if n, a := len(w.rows["tenant-a/customers"]), w.appends; n != 10 || a != 1 {
		t.Errorf("rows = %d after %d appends, want 10 after 1", n, a)
	}
}

func TestAWorkerFailureIsTheWritesFailure(t *testing.T) {
	w := newFakeWorker()
	w.fail = errors.New("the ML worker answered 500: disk full")
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers"})
	err := s.Write(t.Context(), record("tenant-a", map[string]any{"x": 1}))
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want the worker's", err)
	}
	if errors.Is(err, hermod.ErrPermanent) {
		t.Error("a worker failure was marked permanent; another attempt may succeed")
	}
}

func TestPingAsksTheWorker(t *testing.T) {
	w := newFakeWorker()
	s := newSink(t, w, "tenant-a", map[string]string{"dataset": "customers"})
	if err := s.Ping(t.Context()); err != nil || !w.pinged {
		t.Errorf("Ping = %v, pinged = %v", err, w.pinged)
	}
}

func TestConfigIsChecked(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]string
		want string
	}{
		{"no dataset", map[string]string{}, "dataset"},
		{"a dataset name with a path", map[string]string{"dataset": "../x"}, "dataset"},
		{"columns that are not JSON", map[string]string{"dataset": "d", "column_mappings": "age"}, "column"},
		{"a mapping with no column", map[string]string{"dataset": "d", "column_mappings": `[{"source_field":"a"}]`}, "column"},
		{"an unknown mask", map[string]string{"dataset": "d", "mask_fields": "a", "mask_type": "rot13"}, "mask"},
		{"a cap that is not a number", map[string]string{"dataset": "d", "max_rows": "lots"}, "max_rows"},
		{"a negative cap", map[string]string{"dataset": "d", "max_rows": "-1"}, "max_rows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, "tenant-a", newFakeWorker())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one about %q", err, tt.want)
			}
		})
	}
	if _, err := New(map[string]string{"dataset": "d"}, "tenant-a", nil); err == nil {
		t.Error("a sink with no ML worker was built")
	}
}
