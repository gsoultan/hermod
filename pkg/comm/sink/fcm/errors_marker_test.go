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
	"errors"
	"testing"

	"github.com/gsoultan/hermod"
)

// The FCM sink classified its refusals for a caller "that can tell the
// difference", and no caller could: ErrPermanent was this package's own value,
// so the engine retried a refusal that said it could never succeed. Every
// permanent FCM error has to read as hermod.ErrPermanent to anything above it.
func TestPermanentRefusalsReadAsPermanentToTheEngine(t *testing.T) {
	cases := map[string]error{
		"a refusal built here":   permanentf("fcm sink: message %s would be empty", "m-1"),
		"a wrapped refusal":      permanent(errors.New("invalid argument")),
		"an unregistered token":  &UnregisteredTokenError{Token: "tok", Err: errors.New("gone")},
		"the package's own mark": ErrPermanent,
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(err, hermod.ErrPermanent) {
				t.Errorf("%v does not read as hermod.ErrPermanent", err)
			}
			if !errors.Is(err, ErrPermanent) {
				t.Errorf("%v no longer reads as this package's ErrPermanent", err)
			}
		})
	}

	if errors.Is(classify(errors.New("connection reset"), ""), hermod.ErrPermanent) {
		t.Error("a dropped connection was marked permanent; it says nothing about the message")
	}
}
