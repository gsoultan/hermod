// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package stdout

import (
	"context"
	"fmt"

	"github.com/gsoultan/hermod"
)

type StdoutSink struct {
	formatter hermod.Formatter
	logger    hermod.Logger
}

func NewStdoutSink(formatter hermod.Formatter) *StdoutSink {
	return &StdoutSink{
		formatter: formatter,
	}
}

func (s *StdoutSink) SetLogger(l hermod.Logger) {
	s.logger = l
}

func (s *StdoutSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	var data []byte
	var err error

	if s.formatter != nil {
		data, err = s.formatter.Format(msg)
	} else {
		data = msg.Payload()
	}

	if err != nil {
		return fmt.Errorf("failed to format message: %w", err)
	}

	output := string(data)
	fmt.Println(output)

	if s.logger != nil {
		s.logger.Info(output)
	}
	return nil
}

func (s *StdoutSink) Ping(ctx context.Context) error {
	return nil
}

func (s *StdoutSink) Close() error {
	return nil
}
