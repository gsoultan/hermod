// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Granted to no one is any right to use, copy, modify or distribute this
// Except by written permission of the copyright holder named above.
// May not be sold, resold, sublicensed or offered as a paid or hosted
// Business service to any third party. Proprietary and confidential;
// Its full terms are in LICENSE at the repository root. THE SOFTWARE
// IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED.

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
