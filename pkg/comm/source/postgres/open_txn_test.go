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
	"testing"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/jackc/pglogrepl"
)

// A stream that stops inside a transaction may read on to its Commit and
// confirm past it only if every row it received was handed over. A row that
// arrived as the stop did, and was dropped at the handover, has to come back
// on the next start; confirming the commit would discard it for good.
func TestADroppedRowKeepsItsTransactionOpenForReplay(t *testing.T) {
	p := NewPostgresSource("", "slot", "pub", nil, true, "", time.Second)
	p.msgChan = make(chan hermod.Message) // nobody is reading
	p.inTxn = true

	if !p.mayFinishOpenTxn() {
		t.Fatal("a transaction with every row handed over should be finishable")
	}

	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.emit(stopped, pglogrepl.LSN(100), message.AcquireMessage()); err == nil {
		t.Fatal("emit on a stopped stream with no reader handed the row over")
	}
	if p.mayFinishOpenTxn() {
		t.Fatal("a row was dropped at the handover, and the stop would still read on to the " +
			"commit and confirm it: the row would never be delivered")
	}
}
