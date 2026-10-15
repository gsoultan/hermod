// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package filestorage

import (
	"context"
	"io"
)

// Storage defines the interface for file storage operations.
type Storage interface {
	// Save stores the content from the reader and returns a path/URI to the stored file.
	Save(ctx context.Context, name string, r io.Reader) (string, error)
	// GetURL returns a URL or path to access the file.
	GetURL(ctx context.Context, name string) (string, error)
	// Delete removes the file from storage.
	Delete(ctx context.Context, name string) error
	// Type returns the storage type (local, s3).
	Type() string
}
