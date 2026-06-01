package stdout

import (
	"context"
	"fmt"

	"github.com/user/hermod"
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
