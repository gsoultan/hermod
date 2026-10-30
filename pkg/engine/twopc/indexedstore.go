// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package twopc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gsoultan/hermod"
)

// IndexedStore adapts a plain hermod.StateStore into a twopc.Store.
//
// The base interface is Get/Set/Delete with no enumeration, but recovery has to
// answer "what was in flight when we died?" — so this keeps a separate index
// key listing the live record keys, and List reads through it.
//
// The index is the weak point and worth being explicit about: it is a second
// write, so a crash between writing a record and indexing it leaves a record
// that List cannot see. The order below is chosen so that failure is the safe
// one — index first, then the record. An index entry with no record is
// harmless (List skips it and prunes it); a record with no index entry would be
// invisible to recovery, which is the outcome that strands prepared
// transactions.
//
// A backend that can enumerate natively should implement Store directly and
// skip this.
type IndexedStore struct {
	base     hermod.StateStore
	indexKey string

	// mu serialises index read-modify-write. Concurrent transactions would
	// otherwise lose entries, and a lost entry is an invisible in-doubt
	// transaction.
	mu sync.Mutex
}

// NewIndexedStore wraps base. indexKey must be unique per coordinator scope.
func NewIndexedStore(base hermod.StateStore, indexKey string) *IndexedStore {
	return &IndexedStore{base: base, indexKey: indexKey}
}

func (s *IndexedStore) readIndex(ctx context.Context) (map[string]bool, error) {
	raw, err := s.base.Get(ctx, s.indexKey)
	switch {
	case err == nil:
		// fall through
	case errors.Is(err, ErrNotFound):
		// No index yet: this is a fresh store, not a fault.
		return map[string]bool{}, nil
	default:
		// Anything else propagates. It is tempting to treat a read failure as
		// an empty index — but List feeds recovery, and an empty answer there
		// means "nothing is in doubt". Reporting that because the backend was
		// briefly unreachable would skip resolving real prepared transactions
		// and leave them holding locks. Failing loudly is the safe direction.
		return nil, fmt.Errorf("twopc: cannot read the transaction index at %q: %w", s.indexKey, err)
	}
	if len(raw) == 0 {
		return map[string]bool{}, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("twopc: transaction index is corrupt at %q: %w", s.indexKey, err)
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

func (s *IndexedStore) writeIndex(ctx context.Context, keys map[string]bool) error {
	list := make([]string, 0, len(keys))
	for k := range keys {
		list = append(list, k)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.base.Set(ctx, s.indexKey, data)
}

// Set indexes the key before writing the record, so a crash between the two
// cannot hide an in-doubt transaction from recovery.
func (s *IndexedStore) Set(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	if !keys[key] {
		keys[key] = true
		if err := s.writeIndex(ctx, keys); err != nil {
			return fmt.Errorf("twopc: cannot index transaction record: %w", err)
		}
	}
	return s.base.Set(ctx, key, value)
}

func (s *IndexedStore) Get(ctx context.Context, key string) ([]byte, error) {
	return s.base.Get(ctx, key)
}

// Delete removes the record first and the index entry second. The reverse order
// could drop the index entry and then fail, leaving an orphaned record that
// List never returns.
func (s *IndexedStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.base.Delete(ctx, key); err != nil {
		return err
	}
	keys, err := s.readIndex(ctx)
	if err != nil {
		return err
	}
	if keys[key] {
		delete(keys, key)
		return s.writeIndex(ctx, keys)
	}
	return nil
}

// List returns every indexed record under prefix, pruning index entries whose
// record has gone.
func (s *IndexedStore) List(ctx context.Context, prefix string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.list(ctx, prefix, true)
}

// list reads every indexed record under prefix and, when prune is set, drops
// index entries whose record has gone.
func (s *IndexedStore) list(ctx context.Context, prefix string, prune bool) (map[string][]byte, error) {
	keys, err := s.readIndex(ctx)
	if err != nil {
		return nil, err
	}

	out := map[string][]byte{}
	var stale []string
	for k := range keys {
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		v, err := s.base.Get(ctx, k)
		if err != nil || len(v) == 0 {
			// Indexed but absent: the record was deleted and the index write
			// that should have followed did not land. Safe to forget.
			stale = append(stale, k)
			continue
		}
		out[k] = v
	}

	if prune && len(stale) > 0 {
		for _, k := range stale {
			delete(keys, k)
		}
		// Pruning is best effort: the caller already has the records it needs,
		// and a failure here just means the same stale entries are pruned again
		// next time.
		_ = s.writeIndex(ctx, keys)
	}
	return out, nil
}

// readOnlyIndex reads a log another IndexedStore keeps, and never writes it.
//
// Pruning is only safe from the store that writes the log: it indexes a record
// before writing it, so from outside, a record being written looks exactly
// like one deleted, and pruning its entry would hide an in-flight transaction
// from recovery for good.
type readOnlyIndex struct{ s *IndexedStore }

var errReadOnly = errors.New("twopc: this view of the transaction log is read-only")

func (r readOnlyIndex) Get(ctx context.Context, key string) ([]byte, error) {
	return r.s.base.Get(ctx, key)
}
func (readOnlyIndex) Set(context.Context, string, []byte) error { return errReadOnly }
func (readOnlyIndex) Delete(context.Context, string) error      { return errReadOnly }
func (r readOnlyIndex) List(ctx context.Context, prefix string) (map[string][]byte, error) {
	return r.s.list(ctx, prefix, false)
}

// ReadPending lists the transactions in the log a coordinator over
// NewIndexedStore(base, indexKey) keeps for workflowID, without that
// coordinator and without writing anything: a view can call it beside the live
// group, or when the workflow is not running at all.
func ReadPending(ctx context.Context, base hermod.StateStore, indexKey, workflowID string) ([]PendingTx, error) {
	c := &Coordinator{
		store:      readOnlyIndex{s: NewIndexedStore(base, indexKey)},
		workflowID: workflowID,
		now:        time.Now,
	}
	return c.Pending(ctx)
}
