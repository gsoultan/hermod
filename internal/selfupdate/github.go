// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Source lists published releases.
type Source interface {
	Releases(ctx context.Context) ([]Release, error)
}

// GitHub lists releases from the GitHub REST API. The repository is public,
// so no token is sent: the request carries nothing about this installation
// beyond what any HTTP request does.
type GitHub struct {
	Client  *http.Client
	BaseURL string
	Repo    string
}

const (
	maxReleaseListing = 4 << 20
	maxNotes          = 20 << 10
)

// NewGitHub lists releases of gsoultan/hermod.
func NewGitHub() *GitHub {
	return &GitHub{
		Client:  &http.Client{Timeout: 30 * time.Second},
		BaseURL: "https://api.github.com",
		Repo:    "gsoultan/hermod",
	}
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Releases returns the 30 most recent releases.
func (g *GitHub) Releases(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/repos/%s/releases?per_page=30", g.BaseURL, g.Repo), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "hermod-updater")
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach GitHub to check for updates: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
			resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, fmt.Errorf("GitHub's rate limit for anonymous requests from this address is used up; try again within the hour")
		}
		return nil, fmt.Errorf("GitHub answered %s when listing releases", resp.Status)
	}
	var raw []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleaseListing)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("unreadable release listing from GitHub: %w", err)
	}
	out := make([]Release, 0, len(raw))
	for _, r := range raw {
		notes := r.Body
		if len(notes) > maxNotes {
			notes = notes[:maxNotes]
		}
		rel := Release{
			Tag: r.TagName, Name: r.Name, Notes: notes, URL: r.HTMLURL,
			Published: r.PublishedAt, Prerelease: r.Prerelease, Draft: r.Draft,
		}
		for _, a := range r.Assets {
			rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
		}
		out = append(out, rel)
	}
	return out, nil
}
