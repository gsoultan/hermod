package sql

import (
	"testing"
	"time"
)

func TestTracePartitionName_IsOneUTCDay(t *testing.T) {
	got := tracePartitionName(time.Date(2026, 9, 11, 23, 59, 0, 0, time.UTC))
	if want := "message_trace_steps_p20260911"; got != want {
		t.Errorf("tracePartitionName = %q, want %q", got, want)
	}
}

// The DEFAULT partition is the safety valve: rows land there when maintenance
// lags, and dropping it would delete them and make every later insert fail.
// Anything that is not a dated child of ours must be unrecognisable here.
func TestTracePartitionDay_RefusesAnythingButADatedPartition(t *testing.T) {
	for _, name := range []string{
		tracePartitionDefault,
		"message_trace_steps",
		"message_traces",
		"message_trace_steps_pnotadate",
		"message_trace_steps_p2026091",
		"",
	} {
		if _, ok := tracePartitionDay(name); ok {
			t.Errorf("tracePartitionDay(%q) claimed to be a droppable daily partition", name)
		}
	}

	day, ok := tracePartitionDay("message_trace_steps_p20260911")
	if !ok {
		t.Fatal("tracePartitionDay did not recognise its own naming")
	}
	if !day.Equal(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parsed day = %v, want 2026-09-11 UTC", day)
	}
}

func TestTracePartitioningEnabled(t *testing.T) {
	if tracePartitioningEnabled("sqlite") {
		t.Error("partitioning claimed for sqlite, which has no declarative partitioning")
	}
	if !tracePartitioningEnabled("pgx") {
		t.Error("partitioning should default on for PostgreSQL")
	}
	t.Setenv("HERMOD_TRACE_PARTITIONING", "off")
	if tracePartitioningEnabled("pgx") {
		t.Error("HERMOD_TRACE_PARTITIONING=off must opt out")
	}
}

// A partition is dropped only when the whole day is past the cutoff. Dropping
// the day the cutoff falls inside would discard traces still inside the
// retention window — retention turning into data loss.
func TestTracePartitionExpiry_KeepsTheDayTheCutoffFallsInside(t *testing.T) {
	cutoff := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	expired := func(name string) bool {
		day, ok := tracePartitionDay(name)
		if !ok {
			return false
		}
		return !day.AddDate(0, 0, 1).After(cutoff)
	}

	if !expired("message_trace_steps_p20260909") {
		t.Error("a day entirely before the cutoff should be dropped")
	}
	if !expired("message_trace_steps_p20260910") {
		t.Error("the day ending exactly at the cutoff boundary should be dropped")
	}
	if expired("message_trace_steps_p20260911") {
		t.Error("the day the cutoff falls inside must be kept; the range delete trims it")
	}
	if expired("message_trace_steps_p20260912") {
		t.Error("a future day must be kept")
	}
	if expired(tracePartitionDefault) {
		t.Error("the DEFAULT partition must never be dropped")
	}
}
