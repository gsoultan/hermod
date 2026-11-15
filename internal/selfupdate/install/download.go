// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

// Package install downloads a Hermod release, proves it is the release that
// was published, swaps it in for the running binary, and rolls it back when
// the new version cannot stay up.
//
// The order is what makes it safe: nothing on disk changes until the archive
// matches the published SHA-256 and the extracted binary runs and reports the
// version it was downloaded as. The swap is a rename within one folder, so the
// binary on disk is always either the whole old file or the whole new one.
package install

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// ErrTooLarge is a download or extracted file over its size limit.
var ErrTooLarge = errors.New("larger than the limit for a Hermod release")

// releasePath is the only path on github.com a release asset may come from.
const releasePath = "/gsoultan/hermod/releases/download/"

// AllowedURL says whether an asset may be fetched from u. Release assets live
// under the repository's releases on github.com, which redirects to GitHub's
// asset storage on githubusercontent.com. Anything else, plain HTTP, or a URL
// carrying credentials is refused, so a release listing that has been tampered
// with cannot point the download somewhere else.
func AllowedURL(u *url.URL) error {
	if u == nil {
		return errors.New("no URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("refusing to download over %s: only https is allowed", u.Scheme)
	}
	if u.User != nil {
		return errors.New("refusing a download URL that carries credentials")
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "github.com" && strings.HasPrefix(u.Path, releasePath):
		return nil
	case strings.HasSuffix(host, ".githubusercontent.com"):
		return nil
	}
	return fmt.Errorf("refusing to download from %s: Hermod releases come from github.com/gsoultan/hermod", u.Redacted())
}

// NewHTTPClient is the client downloads go through. Every redirect is held to
// AllowedURL as well, not just the first URL.
func NewHTTPClient() *http.Client {
	return &http.Client{
		// A release is ~100 MB; ten minutes is slow-link tolerant without being
		// a download that hangs forever.
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return AllowedURL(req.URL)
		},
	}
}

// Fetcher returns a Fetch that downloads with client.
func Fetcher(client *http.Client) func(ctx context.Context, rawURL string, limit int64, w io.Writer) (int64, error) {
	return func(ctx context.Context, rawURL string, limit int64, w io.Writer) (int64, error) {
		u, err := url.Parse(rawURL)
		if err != nil {
			return 0, err
		}
		if err := AllowedURL(u); err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return 0, err
		}
		req.Header.Set("Accept", "application/octet-stream")
		resp, err := client.Do(req)
		if err != nil {
			return 0, fmt.Errorf("downloading %s: %w", u.Redacted(), err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("downloading %s: %s", u.Redacted(), resp.Status)
		}
		if resp.ContentLength > limit {
			return 0, ErrTooLarge
		}
		n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return n, fmt.Errorf("downloading %s: %w", u.Redacted(), err)
		}
		if n > limit {
			return n, ErrTooLarge
		}
		return n, nil
	}
}

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
