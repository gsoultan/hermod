package googlesheets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/user/hermod"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

type GoogleSheetsSink struct {
	spreadsheetID   string
	sheetRange      string
	operation       string
	credentialsJSON string
	rowIndex        string // template
	columnIndex     string // template
	svc             *sheets.Service
}

func NewGoogleSheetsSink(spreadsheetID, sheetRange, operation, credentialsJSON, rowIndex, columnIndex string) *GoogleSheetsSink {
	return &GoogleSheetsSink{
		spreadsheetID:   spreadsheetID,
		sheetRange:      sheetRange,
		operation:       operation,
		credentialsJSON: credentialsJSON,
		rowIndex:        rowIndex,
		columnIndex:     columnIndex,
	}
}

func (s *GoogleSheetsSink) init(ctx context.Context) error {
	if s.svc != nil {
		return nil
	}

	svc, err := sheets.NewService(ctx, option.WithCredentialsJSON([]byte(s.credentialsJSON)), option.WithScopes(sheets.SpreadsheetsScope))
	if err != nil {
		return fmt.Errorf("failed to create sheets service: %w", err)
	}
	s.svc = svc
	return nil
}

func (s *GoogleSheetsSink) Write(ctx context.Context, msg hermod.Message) error {
	if msg == nil {
		return nil
	}
	if err := s.init(ctx); err != nil {
		return err
	}

	templateData := s.prepareTemplateData(msg)

	spreadsheetID, err := s.render(s.spreadsheetID, templateData)
	if err != nil {
		return err
	}

	sheetRange, err := s.render(s.sheetRange, templateData)
	if err != nil {
		return err
	}

	// For operations that need sheetId, we might need to fetch spreadsheet metadata
	// but let's see if we can use A1 notation or if we need the numeric sheetId.
	// Insert/Delete row/column often needs sheetId (integer).

	sheetID, err := s.getSheetID(ctx, spreadsheetID, sheetRange)
	if err != nil {
		return err
	}

	switch s.operation {
	case "insert_row":
		return s.handleInsertRow(ctx, spreadsheetID, sheetID, templateData, msg)
	case "insert_column":
		return s.handleInsertColumn(ctx, spreadsheetID, sheetID, templateData)
	case "delete_row":
		return s.handleDeleteRow(ctx, spreadsheetID, sheetID, templateData)
	case "delete_column":
		return s.handleDeleteColumn(ctx, spreadsheetID, sheetID, templateData)
	case "append_row":
		return s.handleAppendRow(ctx, spreadsheetID, sheetRange, msg)
	default:
		return fmt.Errorf("unsupported operation: %s", s.operation)
	}
}

func (s *GoogleSheetsSink) handleInsertRow(ctx context.Context, spreadsheetID string, sheetID int64, templateData map[string]interface{}, msg hermod.Message) error {
	idxStr, err := s.render(s.rowIndex, templateData)
	if err != nil {
		return err
	}
	idx, _ := strconv.Atoi(idxStr)

	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				InsertDimension: &sheets.InsertDimensionRequest{
					Range: &sheets.DimensionRange{
						SheetId:    sheetID,
						Dimension:  "ROWS",
						StartIndex: int64(idx),
						EndIndex:   int64(idx + 1),
					},
					InheritFromBefore: false,
				},
			},
		},
	}
	_, err = s.svc.Spreadsheets.BatchUpdate(spreadsheetID, req).Context(ctx).Do()
	if err != nil {
		return err
	}

	// Now update the values in that new row if needed
	// This might be tricky because we need the sheet name and range in A1 notation.
	// For simplicity, if it's insert_row, we just insert the dimension.
	// If user wants to insert AND set data, they should probably use append_row or update_row.
	// But let's try to set data if it's a "create" operation.
	return nil
}

func (s *GoogleSheetsSink) handleAppendRow(ctx context.Context, spreadsheetID, sheetRange string, msg hermod.Message) error {
	data := msg.Data()
	// We need to decide the order of columns.
	// Without a schema, we can just use the keys sorted alphabetically or just the values.
	// Better yet, if the user provides a specific range like Sheet1!A:Z, we can match keys?
	// For now, let's just put all values in a row.

	// If the payload is an array, use it directly
	var values []interface{}
	if payload := msg.After(); payload != nil {
		var decoded interface{}
		if err := json.Unmarshal(payload, &decoded); err == nil {
			if arr, ok := decoded.([]interface{}); ok {
				values = arr
			} else if m, ok := decoded.(map[string]interface{}); ok {
				// Sort keys to be deterministic
				// Or maybe we should support a configuration for column order.
				for _, v := range m {
					values = append(values, v)
				}
			}
		}
	}

	if len(values) == 0 {
		for _, v := range data {
			values = append(values, v)
		}
	}

	rb := &sheets.ValueRange{
		Values: [][]interface{}{values},
	}
	_, err := s.svc.Spreadsheets.Values.Append(spreadsheetID, sheetRange, rb).ValueInputOption("RAW").Context(ctx).Do()
	return err
}

func (s *GoogleSheetsSink) handleInsertColumn(ctx context.Context, spreadsheetID string, sheetID int64, templateData map[string]interface{}) error {
	idxStr, err := s.render(s.columnIndex, templateData)
	if err != nil {
		return err
	}
	idx, _ := strconv.Atoi(idxStr)

	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				InsertDimension: &sheets.InsertDimensionRequest{
					Range: &sheets.DimensionRange{
						SheetId:    sheetID,
						Dimension:  "COLUMNS",
						StartIndex: int64(idx),
						EndIndex:   int64(idx + 1),
					},
					InheritFromBefore: false,
				},
			},
		},
	}
	_, err = s.svc.Spreadsheets.BatchUpdate(spreadsheetID, req).Context(ctx).Do()
	return err
}

func (s *GoogleSheetsSink) handleDeleteRow(ctx context.Context, spreadsheetID string, sheetID int64, templateData map[string]interface{}) error {
	idxStr, err := s.render(s.rowIndex, templateData)
	if err != nil {
		return err
	}
	idx, _ := strconv.Atoi(idxStr)

	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				DeleteDimension: &sheets.DeleteDimensionRequest{
					Range: &sheets.DimensionRange{
						SheetId:    sheetID,
						Dimension:  "ROWS",
						StartIndex: int64(idx),
						EndIndex:   int64(idx + 1),
					},
				},
			},
		},
	}
	_, err = s.svc.Spreadsheets.BatchUpdate(spreadsheetID, req).Context(ctx).Do()
	return err
}

func (s *GoogleSheetsSink) handleDeleteColumn(ctx context.Context, spreadsheetID string, sheetID int64, templateData map[string]interface{}) error {
	idxStr, err := s.render(s.columnIndex, templateData)
	if err != nil {
		return err
	}
	idx, _ := strconv.Atoi(idxStr)

	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				DeleteDimension: &sheets.DeleteDimensionRequest{
					Range: &sheets.DimensionRange{
						SheetId:    sheetID,
						Dimension:  "COLUMNS",
						StartIndex: int64(idx),
						EndIndex:   int64(idx + 1),
					},
				},
			},
		},
	}
	_, err = s.svc.Spreadsheets.BatchUpdate(spreadsheetID, req).Context(ctx).Do()
	return err
}

func (s *GoogleSheetsSink) getSheetID(ctx context.Context, spreadsheetID, sheetRange string) (int64, error) {
	// Extract sheet name from range (e.g., "Sheet1!A1:B2" -> "Sheet1")
	sheetName := sheetRange
	if idx := strings.Index(sheetRange, "!"); idx != -1 {
		sheetName = sheetRange[:idx]
	}
	sheetName = strings.Trim(sheetName, "'")

	ss, err := s.svc.Spreadsheets.Get(spreadsheetID).Context(ctx).Do()
	if err != nil {
		return 0, err
	}

	for _, sheet := range ss.Sheets {
		if sheet.Properties.Title == sheetName {
			return sheet.Properties.SheetId, nil
		}
	}

	if len(ss.Sheets) > 0 {
		return ss.Sheets[0].Properties.SheetId, nil
	}

	return 0, fmt.Errorf("sheet not found: %s", sheetName)
}

func (s *GoogleSheetsSink) prepareTemplateData(msg hermod.Message) map[string]interface{} {
	data := msg.Data()
	templateData := make(map[string]interface{})
	for k, v := range data {
		templateData[k] = v
	}
	templateData["id"] = msg.ID()
	templateData["operation"] = msg.Operation()
	templateData["table"] = msg.Table()
	templateData["schema"] = msg.Schema()
	templateData["metadata"] = msg.Metadata()

	// Unmarshal 'after' if it's JSON
	if after := msg.After(); after != nil {
		var nested map[string]interface{}
		if err := json.Unmarshal(after, &nested); err == nil {
			templateData["after"] = nested
			for k, v := range nested {
				if _, ok := templateData[k]; !ok {
					templateData[k] = v
				}
			}
		}
	}

	return templateData
}

func (s *GoogleSheetsSink) render(tmplStr string, data interface{}) (string, error) {
	if !strings.Contains(tmplStr, "{{") {
		return tmplStr, nil
	}
	tmpl, err := template.New("googlesheets").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *GoogleSheetsSink) Ping(ctx context.Context) error {
	if err := s.init(ctx); err != nil {
		return err
	}
	_, err := s.svc.Spreadsheets.Get(s.spreadsheetID).Context(ctx).Do()
	return err
}

func (s *GoogleSheetsSink) Close() error {
	return nil
}
