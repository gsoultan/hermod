// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package form

import (
	"context"
	"time"
)

type FormSubmission struct {
	ID        string
	Timestamp time.Time
	Path      string
	Data      []byte
	Status    string
}

type FormSubmissionFilter struct {
	Path   string
	Status string
	Limit  int
	Page   int
}

type Storage interface {
	CreateFormSubmission(ctx context.Context, sub FormSubmission) error
	ListFormSubmissions(ctx context.Context, filter FormSubmissionFilter) ([]FormSubmission, int, error)
	UpdateFormSubmissionStatus(ctx context.Context, id string, status string) error
}
