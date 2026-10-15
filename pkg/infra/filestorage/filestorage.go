// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
