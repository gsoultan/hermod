package googlesheets

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gsoultan/hermod"
	"github.com/gsoultan/hermod/pkg/comm/message"
	"github.com/gsoultan/hermod/pkg/infra/ackwatermark"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type GoogleSheetsSource struct {
	spreadsheetID   string
	readRange       string
	credentialsJSON string
	pollInterval    time.Duration
	lastRow         int
	// acked is the cursor that may be persisted. lastRow above advances
	// as rows are read, which is ahead of what has been delivered.
	acked  ackwatermark.Tracker
	logger hermod.Logger
	svc    *sheets.Service
}

func NewGoogleSheetsSource(spreadsheetID, readRange, credentialsJSON string, pollInterval time.Duration) *GoogleSheetsSource {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Minute
	}
	return &GoogleSheetsSource{
		spreadsheetID:   spreadsheetID,
		readRange:       readRange,
		credentialsJSON: credentialsJSON,
		pollInterval:    pollInterval,
	}
}

func (s *GoogleSheetsSource) SetLogger(logger hermod.Logger) {
	s.logger = logger
}

func (s *GoogleSheetsSource) init(ctx context.Context) error {
	if s.svc != nil {
		return nil
	}

	svc, err := sheets.NewService(ctx, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(s.credentialsJSON)), option.WithScopes(sheets.SpreadsheetsReadonlyScope))
	if err != nil {
		return fmt.Errorf("failed to create sheets service: %w", err)
	}
	s.svc = svc
	return nil
}

func (s *GoogleSheetsSource) Read(ctx context.Context) (hermod.Message, error) {
	if err := s.init(ctx); err != nil {
		return nil, err
	}

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		resp, err := s.svc.Spreadsheets.Values.Get(s.spreadsheetID, s.readRange).Context(ctx).Do()
		if err != nil {
			if s.logger != nil {
				s.logger.Error("Failed to fetch sheets values", "error", err)
			}
			return nil, err
		}

		if len(resp.Values) > s.lastRow {
			// New rows found
			rowIndex := s.lastRow
			row := resp.Values[rowIndex]
			s.lastRow++

			data := make(map[string]any)
			for i, val := range row {
				data[fmt.Sprintf("col%d", i)] = val
			}

			payload, _ := json.Marshal(data)
			msg := message.AcquireMessage()
			msg.SetID(fmt.Sprintf("%s-%d", s.spreadsheetID, rowIndex))
			msg.SetOperation(hermod.OpCreate)
			msg.SetTable(s.readRange)
			msg.SetAfter(payload)
			msg.SetMetadata("source", "googlesheets")
			msg.SetMetadata("spreadsheet_id", s.spreadsheetID)
			msg.SetMetadata("row_index", strconv.Itoa(rowIndex))
			// Once this row is acknowledged, every row up to and including
			// it is done — which is exactly what lastRow means.
			s.acked.Emitted(msg.ID(), strconv.Itoa(rowIndex+1))

			return msg, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			continue
		}
	}
}

func (s *GoogleSheetsSource) Ack(ctx context.Context, msg hermod.Message) error {
	// It used to say the watermark was "already updated in Read for
	// simplicity". That is the bug, written down: a row recorded as consumed
	// when it was read is lost if the process stops before it is written.
	if msg != nil {
		s.acked.Ack(msg.ID())
	}
	return nil
}

func (s *GoogleSheetsSource) Ping(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.svc.Spreadsheets.Get(s.spreadsheetID).Context(ctx).Do()
	return err
}

func (s *GoogleSheetsSource) Close() error {
	return nil
}

func (s *GoogleSheetsSource) GetState() map[string]string {
	return map[string]string{
		"last_row": s.acked.Mark(),
	}
}

func (s *GoogleSheetsSource) SetState(state map[string]string) {
	if val, ok := state["last_row"]; ok {
		fmt.Sscanf(val, "%d", &s.lastRow)
		// The mark starts where the read resumes, so it never goes back.
		s.acked.SetMark(val)
	}
}
