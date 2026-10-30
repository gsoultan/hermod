// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package registry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// blockingSourceStorage answers GetSource only once release is closed, so two
// readers can be caught sharing one read.
type blockingSourceStorage struct {
	*pipeStorage
	arrived chan struct{}
	release chan struct{}
	reads   atomic.Int32
}

func (s *blockingSourceStorage) GetSource(ctx context.Context, id string) (storage.Source, error) {
	s.reads.Add(1)
	s.arrived <- struct{}{}
	select {
	case <-s.release:
		return storage.Source{ID: id, Name: "orders", Type: "postgres"}, nil
	case <-ctx.Done():
		return storage.Source{}, ctx.Err()
	}
}

type sourceRead struct {
	src storage.Source
	err error
}

// GetSourceConfig shares one store read between concurrent callers of the same
// id. That read used to run on the first caller's context, so when that caller
// was cancelled -- its run stopped, a rerun replaced it -- every other message
// waiting for the same source config failed with "context canceled" and nothing
// was cached. Every db_lookup message reads its source config, so under load a
// cache miss turned one cancelled message into a batch of failed ones.
func TestGetSourceConfigSharedReadSurvivesTheFirstCallersCancellation(t *testing.T) {
	store := &blockingSourceStorage{
		pipeStorage: newPipeStorage(),
		arrived:     make(chan struct{}, 8),
		release:     make(chan struct{}),
	}
	reg := NewRegistry(store)
	t.Cleanup(reg.Close)

	read := func(ctx context.Context) <-chan sourceRead {
		out := make(chan sourceRead, 1)
		go func() {
			src, err := reg.GetSourceConfig(ctx, "s-orders")
			out <- sourceRead{src, err}
		}()
		return out
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	defer cancelLeader()
	leader := read(leaderCtx)
	select {
	case <-store.arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("the first read never reached the store")
	}

	follower := read(t.Context())
	time.Sleep(50 * time.Millisecond) // let the follower join the in-flight read

	cancelLeader()
	if r := <-leader; !errors.Is(r.err, context.Canceled) {
		t.Errorf("the cancelled caller got err=%v, want its own context.Canceled", r.err)
	}
	close(store.release)

	r := <-follower
	if r.err != nil {
		t.Fatalf("a caller whose context was live failed with %v: another caller's cancellation was reported as its error", r.err)
	}
	if r.src.Name != "orders" {
		t.Errorf("source name = %q, want orders", r.src.Name)
	}
	if n := store.reads.Load(); n != 1 {
		t.Errorf("the store was read %d times, want 1", n)
	}
}
