package file

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/user/hermod"
)

type FileSink struct {
	filename  string
	file      *os.File
	formatter hermod.Formatter
	mu        sync.Mutex
}

func NewFileSink(filename string, formatter hermod.Formatter) (*FileSink, error) {
	return &FileSink{
		filename:  filename,
		formatter: formatter,
	}, nil
}

func (s *FileSink) ensureConnected() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file != nil {
		return nil
	}

	f, err := os.OpenFile(s.filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}

	s.file = f
	return nil
}

func (s *FileSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.ensureConnected(); err != nil {
		return err
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

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.file.Write(data); err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	if _, err := s.file.Write([]byte("\n")); err != nil {
		return fmt.Errorf("failed to write newline to file: %w", err)
	}

	return nil
}

func (s *FileSink) Ping(ctx context.Context) error {
	return s.ensureConnected()
}

func (s *FileSink) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}
