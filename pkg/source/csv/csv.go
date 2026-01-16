package csv

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/user/hermod"
	"github.com/user/hermod/pkg/message"
)

// CSVSource implements the hermod.Source interface for CSV files.
type CSVSource struct {
	filePath  string
	delimiter rune
	hasHeader bool
	file      *os.File
	reader    *csv.Reader
	headers   []string
	finished  bool
}

// NewCSVSource creates a new CSVSource.
func NewCSVSource(filePath string, delimiter rune, hasHeader bool) *CSVSource {
	if delimiter == 0 {
		delimiter = ','
	}
	return &CSVSource{
		filePath:  filePath,
		delimiter: delimiter,
		hasHeader: hasHeader,
	}
}

func (s *CSVSource) init(ctx context.Context) error {
	file, err := os.Open(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to open csv file: %w", err)
	}
	s.file = file
	s.reader = csv.NewReader(file)
	s.reader.Comma = s.delimiter

	if s.hasHeader {
		headers, err := s.reader.Read()
		if err != nil {
			if err == io.EOF {
				s.finished = true
				return nil
			}
			return fmt.Errorf("failed to read csv header: %w", err)
		}
		s.headers = headers
	}

	return nil
}

func (s *CSVSource) Read(ctx context.Context) (hermod.Message, error) {
	if s.finished {
		// To avoid busy loop, we wait until context is cancelled or just return nil
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
			return nil, nil
		}
	}

	if s.reader == nil {
		if err := s.init(ctx); err != nil {
			return nil, err
		}
		if s.finished {
			return nil, nil
		}
	}

	record, err := s.reader.Read()
	if err == io.EOF {
		s.finished = true
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read csv record: %w", err)
	}

	data := make(map[string]interface{})
	if len(s.headers) > 0 {
		for i, val := range record {
			if i < len(s.headers) {
				data[s.headers[i]] = val
			} else {
				data[fmt.Sprintf("column_%d", i)] = val
			}
		}
	} else {
		for i, val := range record {
			data[fmt.Sprintf("column_%d", i)] = val
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("%s-%d", filepath.Base(s.filePath), time.Now().UnixNano()))
	for k, v := range data {
		msg.SetData(k, v)
	}
	msg.SetMetadata("source", "csv")
	msg.SetMetadata("file_path", s.filePath)

	return msg, nil
}

func (s *CSVSource) Ack(ctx context.Context, msg hermod.Message) error {
	// CSV source doesn't support acknowledgement in this simple implementation
	return nil
}

func (s *CSVSource) Ping(ctx context.Context) error {
	_, err := os.Stat(s.filePath)
	if err != nil {
		return fmt.Errorf("csv file not found or inaccessible: %w", err)
	}
	return nil
}

func (s *CSVSource) Close() error {
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func (s *CSVSource) DiscoverDatabases(ctx context.Context) ([]string, error) {
	return []string{"local"}, nil
}

func (s *CSVSource) DiscoverTables(ctx context.Context) ([]string, error) {
	return []string{filepath.Base(s.filePath)}, nil
}

func (s *CSVSource) Sample(ctx context.Context, table string) (hermod.Message, error) {
	// For sample, we can just open the file and read the first record (after header)
	file, err := os.Open(s.filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = s.delimiter

	var headers []string
	if s.hasHeader {
		headers, err = reader.Read()
		if err != nil {
			return nil, err
		}
	}

	record, err := reader.Read()
	if err != nil {
		return nil, err
	}

	data := make(map[string]interface{})
	if len(headers) > 0 {
		for i, val := range record {
			if i < len(headers) {
				data[headers[i]] = val
			} else {
				data[fmt.Sprintf("column_%d", i)] = val
			}
		}
	} else {
		for i, val := range record {
			data[fmt.Sprintf("column_%d", i)] = val
		}
	}

	msg := message.AcquireMessage()
	msg.SetID(fmt.Sprintf("sample-%s", filepath.Base(s.filePath)))
	for k, v := range data {
		msg.SetData(k, v)
	}
	msg.SetMetadata("source", "csv")
	msg.SetMetadata("sample", "true")

	return msg, nil
}
