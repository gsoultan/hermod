// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
