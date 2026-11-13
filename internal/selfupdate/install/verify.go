// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package install

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// ErrChecksum is an archive that does not match the release's published digest.
var ErrChecksum = errors.New("the download does not match the release's published SHA-256")

// ParseChecksums reads goreleaser's checksums.txt: one "<sha256>  <file>" per
// line. A line that does not hold a 64-digit hex digest is an error rather
// than skipped, because a checksum file that is not what we expect is not one
// to trust for the line that does parse.
func ParseChecksums(r io.Reader) (map[string]string, error) {
	sums := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("unreadable checksum line %q", line)
		}
		digest := strings.ToLower(fields[0])
		if b, err := hex.DecodeString(digest); err != nil || len(b) != 32 {
			return nil, fmt.Errorf("not a SHA-256 digest: %q", fields[0])
		}
		// sha256sum marks binary mode with a leading '*'.
		sums[strings.TrimPrefix(fields[1], "*")] = digest
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return sums, nil
}

// ExtractBinary copies the top-level regular file called name out of a
// .tar.gz into dst, refusing more than limit bytes. The release archive also
// carries hermodctl, hermod-edge and docs; only the top-level entry counts.
func ExtractBinary(archivePath, name string, dst io.Writer, limit int64) error {
	f, err := os.Open(archivePath) //nolint:gosec // G304: the temporary file Install downloaded and verified.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("the release archive is not gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the release archive has no %s binary", name)
		}
		if err != nil {
			return fmt.Errorf("reading the release archive: %w", err)
		}
		if h.Typeflag != tar.TypeReg || path.Clean(strings.TrimPrefix(h.Name, "./")) != name {
			continue
		}
		if h.Size > limit {
			return ErrTooLarge
		}
		n, err := io.Copy(dst, io.LimitReader(tr, limit+1))
		if err != nil {
			return fmt.Errorf("extracting %s: %w", name, err)
		}
		if n > limit {
			return ErrTooLarge
		}
		return nil
	}
}
