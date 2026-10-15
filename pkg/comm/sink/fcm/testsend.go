// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package fcm

import (
	"context"
	"errors"
	"fmt"

	"github.com/gsoultan/hermod"
)

// TestSendResult is what a test send did.
type TestSendResult struct {
	// MessageID is FCM's name for the message it accepted.
	MessageID string `json:"message_id"`
	// To says where it went, the way the preview says it.
	To string `json:"to"`
}

// SendTest builds msg into the FCM message a run would send, and really sends
// it: to device when one is named, otherwise to the row's own destination.
//
// It exists because Ping — what Test connection runs — proves the credentials
// and nothing about the message. People read its success as "messages will
// arrive", then met FCM refusing every real one. A test send goes through the
// same build, checks and send as Write, so success means FCM accepted the
// message for a device, and the device should show it.
//
// Exactly one message is sent. A row naming several devices notifies only the
// first, and DryRun is ignored: the test is asked for explicitly, and a
// validated-only send would put nothing on the phone in hand.
func (s *Sink) SendTest(ctx context.Context, msg hermod.Message, device string) (TestSendResult, error) {
	if s.cfg.Action == ActionSubscribe || s.cfg.Action == ActionUnsubscribe {
		return TestSendResult{}, fmt.Errorf("fcm sink: the %s action moves devices between topics and sends no message to test", s.cfg.Action)
	}
	if msg == nil {
		return TestSendResult{}, errors.New("fcm sink: a test send needs a row to build the message from")
	}

	// Built first: a row the sink would refuse is refused without
	// authenticating to anything.
	built, err := s.build(msg)
	if err != nil {
		return TestSendResult{}, err
	}

	c, err := s.ensureConnected(ctx)
	if err != nil {
		return TestSendResult{}, err
	}
	ctx, cancel := s.withTimeout(ctx)
	defer cancel()
	m := built[0]
	if device != "" {
		m.Token, m.Topic, m.Condition = device, "", ""
	}

	id, err := c.Send(ctx, m)
	if err != nil {
		classified := s.classifyFor(err, m, msg.ID())
		if _, ok := asFieldError(classified); ok {
			return TestSendResult{}, classified
		}
		return TestSendResult{}, fmt.Errorf("fcm sink: the test send failed: %w", classified)
	}

	to := "1 device"
	switch {
	case m.Topic != "":
		to = "everyone subscribed to topic " + m.Topic
	case m.Condition != "":
		to = "devices matching " + m.Condition
	}
	return TestSendResult{MessageID: id, To: to}, nil
}
