package excel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	xlsx "github.com/tealeg/xlsx"
)

// The Excel source, against real .xlsx files built with the same library it
// reads them with.
//
// This connector was tiered Beta — "substantial implementation with unit
// tests" — and had no tests at all. The first test written against it showed
// it had never worked: Read re-opened the workbook and returned the first
// data row again on every call, forever. The row cursor was written after
// each emit and then never consulted, so no second row was ever reached, no
// file was ever finished, and a workflow reading a spreadsheet emitted one
// row in an infinite loop.

// writeWorkbook builds an .xlsx with a header row and the given data rows.
func writeWorkbook(t *testing.T, dir, name, sheet string, header []string, rows [][]string) {
	t.Helper()
	f := xlsx.NewFile()
	sh, err := f.AddSheet(sheet)
	if err != nil {
		t.Fatalf("adding sheet: %v", err)
	}
	hr := sh.AddRow()
	for _, h := range header {
		hr.AddCell().Value = h
	}
	for _, r := range rows {
		row := sh.AddRow()
		for _, v := range r {
			row.AddCell().Value = v
		}
	}
	if err := f.Save(filepath.Join(dir, name)); err != nil {
		t.Fatalf("saving workbook: %v", err)
	}
}

func readOne(t *testing.T, s *Source) hermod.Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

// Every row is emitted once, in order, and then the file is finished — not the
// first row over and over.
func TestEveryRowIsEmittedOnceInOrder(t *testing.T) {
	dir := t.TempDir()
	writeWorkbook(t, dir, "people.xlsx", "Sheet1",
		[]string{"name"},
		[][]string{{"ada"}, {"grace"}, {"edsger"}})

	s := New(dir, "people.xlsx", "", 1, 0, 0)

	var got []string
	for i := range 3 {
		msg := readOne(t, s)
		if msg == nil {
			t.Fatalf("row %d: Read returned nil before the file was exhausted", i+1)
		}
		v, _ := msg.Data()["name"].(string)
		got = append(got, v)
	}

	want := []string{"ada", "grace", "edsger"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows arrived as %v, want %v\n"+
				"the row cursor is written after each emit but never consulted, "+
				"so every Read starts from the top and returns the first data row again", got, want)
		}
	}

	// The file is done: the next read finds nothing rather than starting over.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := s.Read(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read past end: %v", err)
	}
	if msg != nil {
		v, _ := msg.Data()["name"].(string)
		t.Errorf("after all rows were emitted, Read produced another row (%q) instead of none", v)
	}
}

// Asking for a sheet by name must read that sheet. The lookup verified the
// name existed and then shadowed the result, so the data always came from the
// first sheet while the message metadata claimed the requested one.
func TestTheNamedSheetIsTheOneThatIsRead(t *testing.T) {
	dir := t.TempDir()

	f := xlsx.NewFile()
	first, err := f.AddSheet("First")
	if err != nil {
		t.Fatalf("adding sheet: %v", err)
	}
	first.AddRow().AddCell().Value = "name"
	first.AddRow().AddCell().Value = "wrong-sheet"

	second, err := f.AddSheet("Second")
	if err != nil {
		t.Fatalf("adding sheet: %v", err)
	}
	second.AddRow().AddCell().Value = "name"
	second.AddRow().AddCell().Value = "right-sheet"

	if err := f.Save(filepath.Join(dir, "two.xlsx")); err != nil {
		t.Fatalf("saving workbook: %v", err)
	}

	s := New(dir, "two.xlsx", "Second", 1, 0, 0)
	msg := readOne(t, s)
	if msg == nil {
		t.Fatal("no row came back at all")
	}
	if v, _ := msg.Data()["name"].(string); v != "right-sheet" {
		t.Errorf("asked for sheet Second, got a row from another sheet: name=%q", v)
	}
}

// Rows must carry the operation the rest of the platform understands.
// hermod.OpCreate is "create"; the source emitted the literal "insert", an
// operation no sink's switch matches, so downstream either dropped or
// misrouted every row this source produced.
func TestRowsCarryAnOperationSinksUnderstand(t *testing.T) {
	dir := t.TempDir()
	writeWorkbook(t, dir, "op.xlsx", "Sheet1",
		[]string{"name"}, [][]string{{"ada"}})

	s := New(dir, "op.xlsx", "", 1, 0, 0)
	msg := readOne(t, s)
	if msg == nil {
		t.Fatal("no row came back at all")
	}
	if msg.Operation() != hermod.OpCreate {
		t.Errorf("rows carry operation %q, want hermod.OpCreate (%q)",
			msg.Operation(), hermod.OpCreate)
	}
}
