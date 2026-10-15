// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package recommend

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

// built is an artifact under construction: the header and every section, so
// a test can corrupt one part and encode it.
type built struct {
	magic   string
	version uint32
	header  map[string]any
	rawHead []byte // replaces the encoded header when set

	userFactors, itemFactors []float32
	nbrItems                 []uint32
	nbrScores                []float32
	seenOff, seenItems       []uint32
	popular                  []uint32
	popularScores            []float32
	trailing                 []byte
}

// factorsArtifact is a valid als artifact: three people, four items, two
// factors. u1 has i0 and i2; u2 has nothing; u3 has i3.
func factorsArtifact() *built {
	return &built{
		magic: "HREC", version: 1,
		header: map[string]any{
			"algorithm": "als", "users": []string{"u1", "u2", "u3"}, "items": []string{"i0", "i1", "i2", "i3"},
			"factors": 2, "neighbors": 0, "seen": 3, "popular": 3,
		},
		userFactors:   []float32{1, 0, 0, 1, 0.5, 0.5},
		itemFactors:   []float32{0.9, 0.1, 0.8, 0.2, 0.1, 0.9, 0.3, 0.3},
		seenOff:       []uint32{0, 2, 2, 3},
		seenItems:     []uint32{0, 2, 3},
		popular:       []uint32{0, 2, 3},
		popularScores: []float32{1, 0.5, 0.5},
	}
}

// neighborsArtifact is a valid item_similarity artifact over the same people
// and items, two neighbors per item; i3 has one, i1 none.
func neighborsArtifact() *built {
	return &built{
		magic: "HREC", version: 1,
		header: map[string]any{
			"algorithm": "item_similarity", "users": []string{"u1", "u2", "u3"}, "items": []string{"i0", "i1", "i2", "i3"},
			"factors": 0, "neighbors": 2, "seen": 3, "popular": 3,
		},
		nbrItems:      []uint32{2, 1, empty, empty, 0, 3, 2, empty},
		nbrScores:     []float32{0.8, 0.4, 0, 0, 0.8, 0.3, 0.6, 0},
		seenOff:       []uint32{0, 2, 2, 3},
		seenItems:     []uint32{0, 2, 3},
		popular:       []uint32{0, 2, 3},
		popularScores: []float32{1, 0.5, 0.5},
	}
}

func (b *built) encode(t testing.TB) []byte {
	t.Helper()
	head := b.rawHead
	if head == nil {
		var err error
		if head, err = json.Marshal(b.header); err != nil {
			t.Fatal(err)
		}
	}
	out := []byte(b.magic)
	out = binary.LittleEndian.AppendUint32(out, b.version)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(head)))
	out = append(out, head...)
	for _, f := range [][]float32{b.userFactors, b.itemFactors} {
		out = appendFloats(out, f)
	}
	out = appendUints(out, b.nbrItems)
	out = appendFloats(out, b.nbrScores)
	out = appendUints(out, b.seenOff)
	out = appendUints(out, b.seenItems)
	out = appendUints(out, b.popular)
	out = appendFloats(out, b.popularScores)
	return append(out, b.trailing...)
}

func appendFloats(out []byte, fs []float32) []byte {
	for _, f := range fs {
		out = binary.LittleEndian.AppendUint32(out, math.Float32bits(f))
	}
	return out
}

func appendUints(out []byte, us []uint32) []byte {
	for _, u := range us {
		out = binary.LittleEndian.AppendUint32(out, u)
	}
	return out
}

func mustParse(t testing.TB, b *built) *Model {
	t.Helper()
	m, err := Parse(b.encode(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestParseReadsBothKindsOfArtifact(t *testing.T) {
	for name, b := range map[string]*built{"als": factorsArtifact(), "item_similarity": neighborsArtifact()} {
		t.Run(name, func(t *testing.T) {
			m := mustParse(t, b)
			if m.Algorithm() != name || m.Users() != 3 || m.Items() != 4 {
				t.Fatalf("algorithm %q, %d users, %d items; want %q, 3, 4", m.Algorithm(), m.Users(), m.Items(), name)
			}
		})
	}
}

// Every way an artifact can be wrong is refused with ErrInvalid, before
// anything is allocated for counts the bytes cannot back.
func TestParseRefusesAMalformedArtifact(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(b *built)
		want   string
	}{
		{"wrong magic", func(b *built) { b.magic = "ONNX" }, "magic"},
		{"newer format", func(b *built) { b.version = 3 }, "format 3"},
		{"header not JSON", func(b *built) { b.rawHead = []byte("{not json") }, "header"},
		{"header longer than the file", func(b *built) { b.rawHead = nil; b.header["users"] = []string{"u1", "u2", "u3"} }, ""},
		{"unknown algorithm", func(b *built) { b.header["algorithm"] = "svd" }, "algorithm"},
		{"no items", func(b *built) { b.header["items"] = []string{} }, "items"},
		{"no people", func(b *built) { b.header["users"] = []string{} }, "people"},
		{"duplicate person", func(b *built) { b.header["users"] = []string{"u1", "u1", "u3"} }, "twice"},
		{"empty item id", func(b *built) { b.header["items"] = []string{"i0", "", "i2", "i3"} }, "empty"},
		{"factors and neighbors", func(b *built) { b.header["neighbors"] = 2 }, "exactly one"},
		{"als without factors", func(b *built) { b.header["factors"] = 0 }, "exactly one"},
		{"too many factors", func(b *built) { b.header["factors"] = MaxFactors + 1 }, "factors"},
		{"negative seen", func(b *built) { b.header["seen"] = -1 }, "seen"},
		{"huge seen", func(b *built) { b.header["seen"] = int64(1) << 62 }, "seen"},
		{"popular over items", func(b *built) { b.header["popular"] = 5 }, "popular"},
		{"truncated", func(b *built) { b.popularScores = b.popularScores[:2] }, "bytes"},
		{"trailing bytes", func(b *built) { b.trailing = []byte{0} }, "bytes"},
		{"NaN factor", func(b *built) { b.itemFactors[3] = float32(math.NaN()) }, "finite"},
		{"infinite popular score", func(b *built) { b.popularScores[0] = float32(math.Inf(1)) }, "finite"},
		{"offsets not starting at 0", func(b *built) { b.seenOff[0] = 1 }, "offsets"},
		{"offsets going back", func(b *built) { b.seenOff = []uint32{0, 2, 1, 3} }, "offsets"},
		{"offsets past seen", func(b *built) { b.seenOff[3] = 4 }, "offsets"},
		{"seen item out of range", func(b *built) { b.seenItems[2] = 4 }, "seen item"},
		{"seen items unsorted", func(b *built) { b.seenItems = []uint32{2, 0, 3} }, "ascending"},
		{"seen item twice", func(b *built) { b.seenItems = []uint32{2, 2, 3} }, "ascending"},
		{"popular item out of range", func(b *built) { b.popular[1] = 9 }, "popular item"},
		{"popular item twice", func(b *built) { b.popular[1] = 0 }, "twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := factorsArtifact()
			tc.mutate(b)
			raw := b.encode(t)
			if tc.name == "header longer than the file" {
				binary.LittleEndian.PutUint32(raw[8:], uint32(len(raw)))
			}
			_, err := Parse(raw)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestParseRefusesBadNeighborLists(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(b *built)
		want   string
	}{
		{"neighbor out of range", func(b *built) { b.nbrItems[0] = 7 }, "neighbor"},
		{"slot used after an empty one", func(b *built) { b.nbrItems[3] = 0 }, "after an empty"},
		{"NaN similarity", func(b *built) { b.nbrScores[0] = float32(math.NaN()) }, "finite"},
		{"item_similarity without neighbors", func(b *built) { b.header["neighbors"] = 0 }, "exactly one"},
		{"too many neighbors", func(b *built) { b.header["neighbors"] = MaxNeighbors + 1 }, "neighbors"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := neighborsArtifact()
			tc.mutate(b)
			_, err := Parse(b.encode(t))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse = %v, want ErrInvalid mentioning %q", err, tc.want)
			}
		})
	}
}

func TestParseRefusesAnArtifactOverTheSizeLimit(t *testing.T) {
	raw := make([]byte, MaxArtifactBytes+1)
	copy(raw, "HREC")
	if _, err := Parse(raw); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Parse = %v, want ErrInvalid over the limit", err)
	}
}

func TestParseRefusesEveryTruncation(t *testing.T) {
	for _, b := range []*built{factorsArtifact(), neighborsArtifact()} {
		raw := b.encode(t)
		for n := range len(raw) {
			if _, err := Parse(raw[:n]); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse of the first %d of %d bytes = %v, want ErrInvalid", n, len(raw), err)
			}
		}
	}
}
