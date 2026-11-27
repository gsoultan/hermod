// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package postgres

import (
	"context"

	"github.com/gsoultan/hermod"
)

// SetSharedSlotMember names the workflow this source reads for. Workflows on
// a shared slot are told apart by it, so one that stops is held for and
// replayed to when it starts again; without it, a source that stops resumes
// where the shared stream is.
func (p *PostgresSource) SetSharedSlotMember(workflowID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.member = hubMember(workflowID)
}

// joinHub puts this CDC source on the hub for its slot instead of opening a
// replication connection of its own. See slotHub.
func (p *PostgresSource) joinHub(ctx context.Context) error {
	p.logMu.RLock()
	logger := p.logger
	p.logMu.RUnlock()
	tables := p.tableList()
	created := false
	newStream := func(logs hermod.Logger) hubStream {
		created = true
		return p.newHubStream(logs)
	}
	sub, err := slotHubs.join(ctx, hubKey(p.connString, p.slotName), p.publicationName, tables, logger, newStream, p.member)
	if err != nil {
		p.log("ERROR", "PostgresSource initialization failed",
			"slot", p.slotName, "publication", p.publicationName, "error", err)
		return err
	}
	if stream, ok := sub.hub.stream.(*PostgresSource); ok {
		p.mu.Lock()
		persistent, initialLoad := p.persistentSlot, p.initialLoad
		p.mu.Unlock()
		// A slot any workflow wants kept outlives the workflows that did not.
		if persistent {
			stream.SetPersistentSlot(true)
		}
		if initialLoad && !created {
			p.log("INFO", "Initial load skipped: another workflow already streams this replication slot, "+
				"and an initial load happens only when the slot is created", "slot", p.slotName)
		}
	}
	p.mu.Lock()
	p.sub = sub
	p.initialized = true
	p.mu.Unlock()
	p.log("INFO", "PostgresSource joined the shared stream of its replication slot",
		"slot", p.slotName, "publication", p.publicationName)
	return nil
}

// newHubStream builds the PostgresSource a new hub streams through, from the
// first workflow to join it.
func (p *PostgresSource) newHubStream(logs hermod.Logger) hubStream {
	p.mu.Lock()
	persistent, initialLoad, afterEmit := p.persistentSlot, p.initialLoad, p.afterEmit
	p.mu.Unlock()
	stream := NewPostgresSource(p.connString, p.slotName, p.publicationName, p.tableList(), true, "", p.pollInterval)
	stream.hubOwned = true
	stream.persistentSlot = persistent
	stream.initialLoad = initialLoad
	stream.afterEmit = afterEmit
	stream.logger = logs
	return stream
}

func (p *PostgresSource) subscription() *hubSubscription {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sub
}

// leaveHub takes this source off its hub's books, and returns what to leave.
func (p *PostgresSource) leaveHub() *hubSubscription {
	p.mu.Lock()
	defer p.mu.Unlock()
	sub := p.sub
	if sub != nil {
		p.sub = nil
		p.initialized = false
	}
	return sub
}

// hubSource is the PostgresSource streaming this source's slot, when it is
// on a hub.
func (p *PostgresSource) hubSource() *PostgresSource {
	sub := p.subscription()
	if sub == nil {
		return nil
	}
	stream, _ := sub.hub.stream.(*PostgresSource)
	return stream
}
