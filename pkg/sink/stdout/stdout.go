package stdout

import (
	"context"
	"fmt"

	"github.com/user/hermod"
)

type StdoutSink struct {
	formatter hermod.Formatter
}

func NewStdoutSink(formatter hermod.Formatter) *StdoutSink {
	return &StdoutSink{
		formatter: formatter,
	}
}

func (s *StdoutSink) Write(ctx context.Context, msg hermod.Message) error {
	if s.formatter != nil {
		data, err := s.formatter.Format(msg)
		if err != nil {
			return fmt.Errorf("failed to format message: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}

	fmt.Printf("CDC Message [%s] - ID: %s, Table: %s.%s, Before: %s, After: %s, Metadata: %v\n",
		msg.Operation(), msg.ID(), msg.Schema(), msg.Table(), string(msg.Before()), string(msg.After()), msg.Metadata())
	return nil
}

func (s *StdoutSink) Ping(ctx context.Context) error {
	return nil
}

func (s *StdoutSink) Close() error {
	return nil
}
