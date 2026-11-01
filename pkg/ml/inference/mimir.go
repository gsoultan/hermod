// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Governed by the Hermod Proprietary License; see LICENSE for the terms.
// Every use, copy, change or distribution needs written permission.
// May not be sold, resold or sublicensed to any third party.
// Bundling it into a paid or hosted service for others is prohibited.
// It is proprietary and confidential to Gembit Soultan Shirazi.
// There is no warranty of any kind; it is provided "AS IS".

package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	mimirsdk "github.com/gsoultan/mimir-server-sdk"
	"golang.org/x/sync/errgroup"
)

// BackendMimir is a mimir-server (github.com/gsoultan/mimir-server), spoken
// to through its Go SDK over Connect. Target.Model is the model's id on the
// server (an opaque id, not its name) and Target.Token the API key, which the
// server requires: it answers no prediction anonymously. A mimir model is
// asked one row at a time, by column name, so Target.Features (when set) names
// the columns sent; without it every field of the row goes.
const BackendMimir Backend = "mimir"

// mimirConcurrency bounds how many rows of one call are in flight at once.
// mimir answers one example per request, so a batch is a burst of requests;
// this keeps it from flooding a server that also serves people.
const mimirConcurrency = 4

// RemoteModel is one model as a mimir server lists it, the part Hermod shows
// when a model is connected: enough to pick it and to pre-fill its inputs.
type RemoteModel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Architecture string `json:"architecture,omitempty"`
	Description  string `json:"description,omitempty"`
	// Ready says the model answers now. Status explains why not, in the
	// server's words, when it does not.
	Ready    bool           `json:"ready"`
	Status   string         `json:"status,omitempty"`
	Accuracy float64        `json:"accuracy,omitempty"`
	Labels   []string       `json:"labels,omitempty"`
	Columns  []RemoteColumn `json:"columns,omitempty"`
}

// RemoteColumn is one input of a model taught from a table: its name and
// kind ("number", "category", "yesno", "text" or "photo").
type RemoteColumn struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

// validateMimir is Target.Validate's part for a mimir server.
func (t Target) validateMimir() error {
	if !validPathSegment(t.Model) {
		return errors.New("a mimir model is called by its id on the server: pick one")
	}
	if t.InputName != "" {
		return errors.New("a mimir model is asked by column name: it takes no input tensor name")
	}
	return nil
}

// mimirClient builds an SDK client for t over Hermod's HTTP client. The SDK's
// own timeout is off: Predict already bounded ctx by the target's timeout.
func (c *Client) mimirClient(url, token string) (*mimirsdk.Client, error) {
	if token == "" {
		return nil, errors.New("a mimir server needs an API key: name the secret that holds it")
	}
	client, err := mimirsdk.New(url,
		mimirsdk.WithAPIKey(token),
		mimirsdk.WithHTTPClient(c.http),
		mimirsdk.WithTimeout(0),
		mimirsdk.WithUserAgent("hermod"),
	)
	if err != nil {
		return nil, fmt.Errorf("the mimir server address: %w", err)
	}
	return client, nil
}

// predictMimir asks the model about each row and keeps the answers in the
// rows' order. A label model answers with "prediction" (the likeliest label),
// "confidence" (its probability), "guesses" (every label with its probability,
// likeliest first) and "prediction_id"; a number model with "prediction" (the
// number) and "prediction_id". The id is what SendFeedback takes later.
func (c *Client) predictMimir(ctx context.Context, t Target, rows []Row) ([]Row, error) {
	client, err := c.mimirClient(t.URL, t.Token)
	if err != nil {
		return nil, err
	}
	out := make([]Row, len(rows))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(mimirConcurrency)
	for i, row := range rows {
		g.Go(func() error {
			ans, err := client.Models.PredictFields(gctx, t.Model, mimirFields(row, t.Features))
			if err != nil {
				return fmt.Errorf("row %d: the mimir server did not answer: %w", i, err)
			}
			out[i] = mimirRow(ans)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// mimirFields is the row as mimir takes it: every value as text, by column.
// With features declared only those columns go, and a missing one goes empty,
// which the server reads as the column's fill value.
func mimirFields(row Row, features []string) map[string]string {
	if len(features) == 0 {
		fields := make(map[string]string, len(row))
		for k, v := range row {
			fields[k] = mimirText(v)
		}
		return fields
	}
	fields := make(map[string]string, len(features))
	for _, f := range features {
		fields[f] = mimirText(row[f])
	}
	return fields
}

// mimirText is v as the text a CSV cell would hold.
func mimirText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	case fmt.Stringer:
		return x.String()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(raw)
}

// mimirRow turns one answer into the row the rest of Hermod reads.
func mimirRow(ans *mimirsdk.PredictResponse) Row {
	row := Row{"prediction_id": ans.GetPredictionId()}
	if ans.GetNumber() {
		row["prediction"] = ans.GetValue()
		return row
	}
	guesses := ans.GetGuesses()
	if len(guesses) == 0 {
		row["prediction"] = nil
		return row
	}
	row["prediction"] = guesses[0].GetLabel()
	row["confidence"] = float64(guesses[0].GetProbability())
	all := make([]map[string]any, len(guesses))
	for i, g := range guesses {
		all[i] = map[string]any{"label": g.GetLabel(), "probability": float64(g.GetProbability())}
	}
	row["guesses"] = all
	return row
}

// ListMimirModels is what the server at url offers the key's holder, for a
// person to pick one from when connecting a model.
func (c *Client) ListMimirModels(ctx context.Context, url, token string) ([]RemoteModel, error) {
	client, err := c.mimirClient(url, token)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	models, err := client.Models.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("the mimir server did not list its models: %w", err)
	}
	out := make([]RemoteModel, 0, len(models))
	for _, m := range models {
		rm := RemoteModel{
			ID: m.GetId(), Name: m.GetName(), Architecture: m.GetArchitecture(), Description: m.GetDescription(),
			Ready: m.GetStatus() == mimirsdk.ModelStatusReady, Accuracy: m.GetAccuracy(), Labels: m.GetLabels(),
		}
		if !rm.Ready {
			rm.Status = m.GetStatusDetail()
			if rm.Status == "" {
				rm.Status = "not ready"
			}
		}
		for _, col := range m.GetColumns() {
			rm.Columns = append(rm.Columns, RemoteColumn{Name: col.GetName(), Kind: col.GetKind()})
		}
		out = append(out, rm)
	}
	return out, nil
}

// ErrNotMimir is returned when feedback or an explanation is asked of a model
// that is not on a mimir server: the other protocols have no such call.
var ErrNotMimir = errors.New("only a model on a mimir server can do this")

// SendFeedback tells the mimir server what the right answer to one earlier
// prediction was: predictionID is the "prediction_id" that prediction came
// with, actual the real label, or the real number for a model that answers
// with one. It reports whether the model had been right. mimir keeps a
// prediction for feedback for 30 days; after that the server answers not
// found.
func (c *Client) SendFeedback(ctx context.Context, t Target, predictionID string, actual any) (bool, error) {
	if t.Backend != BackendMimir {
		return false, ErrNotMimir
	}
	if err := t.Validate(); err != nil {
		return false, err
	}
	if predictionID == "" {
		return false, errors.New("feedback needs the prediction id the answer came with")
	}
	client, err := c.mimirClient(t.URL, t.Token)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	var right bool
	switch v := actual.(type) {
	case float64:
		right, err = client.Models.SendFeedbackValue(ctx, predictionID, v)
	case float32:
		right, err = client.Models.SendFeedbackValue(ctx, predictionID, float64(v))
	case int:
		right, err = client.Models.SendFeedbackValue(ctx, predictionID, float64(v))
	case int64:
		right, err = client.Models.SendFeedbackValue(ctx, predictionID, float64(v))
	case json.Number:
		n, convErr := v.Float64()
		if convErr != nil {
			return false, fmt.Errorf("the real answer %q is not a number", v)
		}
		right, err = client.Models.SendFeedbackValue(ctx, predictionID, n)
	default:
		right, err = client.Models.SendFeedback(ctx, predictionID, mimirText(actual))
	}
	if err != nil {
		return false, fmt.Errorf("the mimir server did not take the feedback: %w", err)
	}
	return right, nil
}

// Explain asks why the model answers as it does for one row: each column's
// effect on the answer against a typical row. label names the answer to
// explain for a label model, empty for the one it gives. The row read back
// has "label", "number", "value", "typical", "exact" and "contributions",
// each contribution {"column", "value", "typical", "effect"}, biggest effect
// first. Effects are in chance (0.18 is 18 points) or in the number's units.
func (c *Client) Explain(ctx context.Context, t Target, row Row, label string) (Row, error) {
	if t.Backend != BackendMimir {
		return nil, ErrNotMimir
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	client, err := c.mimirClient(t.URL, t.Token)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, t.timeout())
	defer cancel()
	why, err := client.Models.Explain(ctx, t.Model, mimirFields(row, t.Features), label)
	if err != nil {
		return nil, fmt.Errorf("the mimir server did not explain the answer: %w", err)
	}
	contributions := make([]map[string]any, len(why.GetContributions()))
	for i, con := range why.GetContributions() {
		contributions[i] = map[string]any{
			"column": con.GetColumn(), "value": con.GetValue(), "typical": con.GetTypical(), "effect": con.GetEffect(),
		}
	}
	return Row{
		"label": why.GetLabel(), "number": why.GetNumber(), "value": why.GetValue(), "typical": why.GetTypical(),
		"exact": why.GetExact(), "contributions": contributions,
	}, nil
}

// timeout is the target's timeout, or the default.
func (t Target) timeout() time.Duration {
	if t.Timeout <= 0 {
		return DefaultTimeout
	}
	return t.Timeout
}
