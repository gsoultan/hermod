// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
