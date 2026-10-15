// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

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
