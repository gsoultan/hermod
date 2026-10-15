// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package evaluator

import (
	"context"

	"github.com/gsoultan/hermod"
)

type OutboxTransactionalSource struct {
	hermod.Source
}

func (s *OutboxTransactionalSource) Begin(ctx context.Context) error {
	if t, ok := s.Source.(hermod.Transactional); ok {
		return t.Begin(ctx)
	}
	return nil
}

func (s *OutboxTransactionalSource) Commit(ctx context.Context) error {
	if t, ok := s.Source.(hermod.Transactional); ok {
		return t.Commit(ctx)
	}
	return nil
}

func (s *OutboxTransactionalSource) Rollback(ctx context.Context) error {
	if t, ok := s.Source.(hermod.Transactional); ok {
		return t.Rollback(ctx)
	}
	return nil
}
