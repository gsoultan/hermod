// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package spool

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const segSuffix = ".seg"

// segment is one append-only file of records. Its name is the sequence
// number of its first record, zero-padded so names sort in order.
type segment struct {
	path  string
	first uint64 // sequence number the segment was created for
	last  uint64 // last sequence number in it; meaningless while count == 0
	count uint64
	size  int64
}

func segmentPath(dir string, first uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d%s", first, segSuffix))
}

// listSegments returns the segment files in dir in sequence order.
func listSegments(dir string) ([]*segment, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var segs []*segment
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, segSuffix) {
			continue
		}
		first, err := strconv.ParseUint(strings.TrimSuffix(name, segSuffix), 10, 64)
		if err != nil {
			continue
		}
		segs = append(segs, &segment{path: filepath.Join(dir, name), first: first})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].first < segs[j].first })
	return segs, nil
}

// scan reads every whole record in the segment, setting count, last and size,
// and truncates a torn or corrupt tail so the next append starts clean.
func (s *segment) scan() error {
	f, err := os.OpenFile(s.path, os.O_RDWR, 0) //nolint:gosec // G304: a segment the spool itself listed in its own directory
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	var off int64
	for {
		rec, n, err := readFrame(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if terr := f.Truncate(off); terr != nil {
				return terr
			}
			if serr := f.Sync(); serr != nil {
				return serr
			}
			break
		}
		off += n
		s.count++
		s.last = rec.Seq
	}
	s.size = off
	return nil
}

// readAfter reads up to limit records with a sequence number above after,
// starting at byte offset from. It returns the records and the offset just
// past the last one returned.
func (s *segment) readAfter(after uint64, from int64, limit int) ([]Record, int64, error) {
	f, err := os.Open(s.path) //nolint:gosec // G304: a segment the spool itself created
	if err != nil {
		return nil, from, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, from, err
	}
	r := bufio.NewReader(io.LimitReader(f, s.size-from))
	var out []Record
	off := from
	for len(out) < limit {
		rec, n, err := readFrame(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, off, fmt.Errorf("spool: read %s at %d: %w", filepath.Base(s.path), off, err)
		}
		off += n
		if rec.Seq > after {
			out = append(out, rec)
		}
	}
	return out, off, nil
}
