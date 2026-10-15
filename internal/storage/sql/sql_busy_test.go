// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package sql

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsSQLiteBusyError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("some other error"), false},
		{errors.New("database is locked (5) (SQLITE_BUSY)"), true},
		{errors.New("SQLITE_BUSY: database is locked"), true},
	}
	for i, c := range cases {
		if got := isSQLiteBusyError(c.err); got != c.want {
			t.Fatalf("case %d: want %v got %v", i, c.want, got)
		}
	}
}

func TestExecWithRetry_BusyThenSuccess(t *testing.T) {
	s := &sqlStorage{}
	attempts := 0
	fn := func() error {
		attempts++
		if attempts < 3 {
			return errors.New("database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := s.execWithRetry(ctx, fn); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestExecWithRetry_NonBusyError(t *testing.T) {
	s := &sqlStorage{}
	exp := errors.New("boom")
	fn := func() error { return exp }
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := s.execWithRetry(ctx, fn); !errors.Is(err, exp) {
		t.Fatalf("expected %v, got %v", exp, err)
	}
}

func TestExecWithRetry_ContextCancel(t *testing.T) {
	s := &sqlStorage{}
	fn := func() error { return errors.New("database is locked (5) (SQLITE_BUSY)") }
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := s.execWithRetry(ctx, fn); err == nil {
		t.Fatalf("expected context error, got nil")
	}
}
