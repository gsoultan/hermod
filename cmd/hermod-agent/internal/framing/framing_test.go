// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package framing

import (
	"reflect"
	"testing"

	pb "github.com/gsoultan/hermod/pkg/agent/proto"
)

func mustNew(t *testing.T, f *pb.Framing) Framer {
	t.Helper()
	fr, err := New(f)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fr
}

func strs(frames [][]byte) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, string(f))
	}
	return out
}

// feed sends input in the given chunk sizes, the way a serial port delivers
// a few bytes at a time, and collects every frame.
func feed(fr Framer, in string, chunk int) []string {
	var out []string
	for len(in) > 0 {
		n := min(chunk, len(in))
		out = append(out, strs(fr.Feed([]byte(in[:n])))...)
		in = in[n:]
	}
	return out
}

func TestLineFramingAcceptsLFCRAndCRLF(t *testing.T) {
	for _, chunk := range []int{1, 2, 3, 100} {
		fr := mustNew(t, nil)
		got := feed(fr, "a\nb\r\nc\rd\r\n\r\ne", chunk)
		if want := []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
			t.Errorf("chunk %d: frames = %q, want %q", chunk, got, want)
		}
	}
}

func TestDelimiterFraming(t *testing.T) {
	fr := mustNew(t, &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_DELIMITER, Delimiter: []byte("||")})
	got := feed(fr, "one||two|||three", 1)
	if want := []string{"one", "two", "|three"}[:2]; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
}

func TestFixedLengthFraming(t *testing.T) {
	fr := mustNew(t, &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_FIXED_LENGTH, Length: 3})
	got := feed(fr, "abcdefgh", 2)
	if want := []string{"abc", "def"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
}

func TestStartEndFramingDiscardsBytesOutsideAFrame(t *testing.T) {
	fr := mustNew(t, &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_START_END, StartBytes: []byte{0x02}, EndBytes: []byte{0x03}})
	got := feed(fr, "noise\x0212.40\x03junk\x02x\x02y\x03", 1)
	// A second start inside a frame restarts it: the partial "x" was noise.
	if want := []string{"12.40", "y"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
}

func TestLengthPrefixFraming(t *testing.T) {
	fr := mustNew(t, &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_LENGTH_PREFIX, PrefixSize: 2})
	got := feed(fr, "\x00\x03abc\x00\x00\x00\x02de", 1)
	if want := []string{"abc", "de"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
}

func TestIdleGapFramingEmitsOnIdle(t *testing.T) {
	fr := mustNew(t, &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_IDLE_GAP, IdleMs: 50})
	if got := feed(fr, "partial ", 1); len(got) != 0 {
		t.Fatalf("frames before idle = %q", got)
	}
	_ = fr.Feed([]byte("reading"))
	if got := strs(fr.Idle()); !reflect.DeepEqual(got, []string{"partial reading"}) {
		t.Fatalf("Idle = %q", got)
	}
	if got := fr.Idle(); len(got) != 0 {
		t.Fatalf("second Idle = %q", got)
	}
}

func TestIdleDoesNothingForOtherFramings(t *testing.T) {
	fr := mustNew(t, nil)
	_ = fr.Feed([]byte("half a line"))
	if got := fr.Idle(); len(got) != 0 {
		t.Fatalf("Idle on line framing = %q", got)
	}
	if got := strs(fr.Feed([]byte("\n"))); !reflect.DeepEqual(got, []string{"half a line"}) {
		t.Fatalf("frames = %q", got)
	}
}

// A device that never sends a line ending must not grow the agent's memory
// without bound: the oversize record is dropped, counted, and framing resumes
// at the next line.
func TestOversizeRecordIsDroppedAndCounted(t *testing.T) {
	fr := mustNew(t, &pb.Framing{MaxRecordBytes: 4})
	got := feed(fr, "abcdefghij\nok\n", 3)
	if want := []string{"ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
	if fr.Oversize() != 1 {
		t.Fatalf("Oversize = %d, want 1", fr.Oversize())
	}
}

func TestOversizeLengthPrefixIsSkipped(t *testing.T) {
	fr := mustNew(t, &pb.Framing{Kind: pb.FramingKind_FRAMING_KIND_LENGTH_PREFIX, PrefixSize: 1, MaxRecordBytes: 2})
	got := feed(fr, "\x05abcde\x02ok", 1)
	if want := []string{"ok"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("frames = %q, want %q", got, want)
	}
	if fr.Oversize() != 1 {
		t.Fatalf("Oversize = %d, want 1", fr.Oversize())
	}
}

func TestFramesDoNotAliasTheInputBuffer(t *testing.T) {
	fr := mustNew(t, nil)
	buf := []byte("abc\n")
	frames := fr.Feed(buf)
	buf[0] = 'X'
	if string(frames[0]) != "abc" {
		t.Fatalf("frame changed with the input buffer: %q", frames[0])
	}
}

func TestInvalidSettingsAreRefused(t *testing.T) {
	bad := []*pb.Framing{
		{Kind: pb.FramingKind_FRAMING_KIND_DELIMITER},
		{Kind: pb.FramingKind_FRAMING_KIND_FIXED_LENGTH},
		{Kind: pb.FramingKind_FRAMING_KIND_START_END, StartBytes: []byte{2}},
		{Kind: pb.FramingKind_FRAMING_KIND_LENGTH_PREFIX, PrefixSize: 3},
		{Kind: pb.FramingKind_FRAMING_KIND_IDLE_GAP},
		{Kind: pb.FramingKind(99)},
	}
	for _, f := range bad {
		if _, err := New(f); err == nil {
			t.Errorf("New(%v) accepted", f)
		}
	}
}

// A record has to fit in one message to Hermod, so the limit a device's
// settings may ask for is capped.
func TestMaxRecordBytesIsCapped(t *testing.T) {
	if _, err := New(&pb.Framing{MaxRecordBytes: MaxRecordBytesLimit + 1}); err == nil {
		t.Fatalf("a record limit over %d bytes was accepted", MaxRecordBytesLimit)
	}
	if _, err := New(&pb.Framing{MaxRecordBytes: MaxRecordBytesLimit}); err != nil {
		t.Fatalf("the largest allowed limit was refused: %v", err)
	}
}
