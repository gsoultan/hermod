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

import "context"

// Crash points.
//
// Recovery is the whole reason this package exists, and it can only be tested
// by stopping the protocol part-way and starting a fresh coordinator over the
// same log — which is exactly what a restart is. These helpers stop at the two
// states that behave differently on recovery:
//
//	prepareOnly      -> PREPARED    (no decision logged; recovery must abort)
//	prepareAndDecide -> COMMITTING  (decision logged; recovery must commit)
//
// They deliberately reuse the production paths rather than writing records by
// hand, so a change to the protocol cannot leave the tests asserting against a
// shape the coordinator no longer produces.

// prepareOnly runs work and collects every vote, then stops. The transaction is
// left PREPARED with no decision: participants are in doubt holding locks.
func (c *Coordinator) prepareOnly(ctx context.Context, participants []Participant, work func(context.Context) error) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	rec, err := c.prepareAll(ctx, participants, work)
	if err != nil {
		return "", err
	}
	return c.key(rec.TxID), nil
}

// prepareAndDecide goes one step further and makes the commit decision durable,
// then stops before telling any participant. Recovery is bound by the decision.
func (c *Coordinator) prepareAndDecide(ctx context.Context, participants []Participant, work func(context.Context) error) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	rec, err := c.prepareAll(ctx, participants, work)
	if err != nil {
		return "", err
	}

	rec.State = stateCommitting
	if err := c.save(ctx, rec); err != nil {
		return "", err
	}
	return c.key(rec.TxID), nil
}
