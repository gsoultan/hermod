package table

import (
	"reflect"
	"testing"
)

func TestTakePicksRowsInOrder(t *testing.T) {
	tb, err := FromRecords([]map[string]any{{"a": 1.0, "s": "x"}, {"a": nil, "s": "y"}, {"a": 3.0, "s": nil}})
	if err != nil {
		t.Fatal(err)
	}
	got := tb.Take([]int{2, 0}).Records(0, 2)
	want := []map[string]any{{"a": 3.0, "s": nil}, {"a": 1.0, "s": "x"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Take = %v, want %v", got, want)
	}
}
