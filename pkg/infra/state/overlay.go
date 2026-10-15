// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

package state

import (
	"context"
	"slices"
	"sync"

	"github.com/gsoultan/hermod"
)

// NewOverlay returns a state store that reads through to base and keeps every
// write to itself. A preview runs against one laid over the live store: a
// lookup sees what running workflows have stored -- the answer an operator
// previews the node to check -- while a store, a counter bump or a delete stays
// in the overlay and is dropped with it, so no preview changes the state a
// running workflow reads.
func NewOverlay(base hermod.StateStore) hermod.StateStore {
	return &overlay{base: base, writes: map[string][]byte{}, deleted: map[string]bool{}}
}

type overlay struct {
	base hermod.StateStore

	mu      sync.Mutex
	writes  map[string][]byte
	deleted map[string]bool
}

func (o *overlay) Get(ctx context.Context, key string) ([]byte, error) {
	o.mu.Lock()
	if v, ok := o.writes[key]; ok {
		o.mu.Unlock()
		return slices.Clone(v), nil
	}
	gone := o.deleted[key]
	o.mu.Unlock()
	if gone {
		return nil, nil
	}
	return o.base.Get(ctx, key)
}

func (o *overlay) Set(_ context.Context, key string, value []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if value == nil {
		value = []byte{}
	}
	o.writes[key] = slices.Clone(value)
	delete(o.deleted, key)
	return nil
}

func (o *overlay) Delete(_ context.Context, key string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.writes, key)
	o.deleted[key] = true
	return nil
}
