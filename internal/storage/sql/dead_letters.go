// Copyright (c) 2026 Gembit Soultan Shirazi. All rights reserved.
// SPDX-License-Identifier: LicenseRef-Hermod-Proprietary
//
// Hermod: real-time Postgres CDC and data pipelines in one Go binary.
// Each file is proprietary and confidential; see LICENSE for the terms.
// Resale, sublicensing or selling it to any third party is prohibited.
// Making copies, modifying or distributing it needs written permission
// Of the copyright holder, Gembit Soultan Shirazi.
// Distributed "AS IS", WITHOUT WARRANTY OF ANY KIND, express or implied.

package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

func (s *sqlStorage) PutDeadLetter(ctx context.Context, d storage.DeadLetter) error {
	d = storage.CapDeadLetter(d)
	if err := storage.ValidateDeadLetter(d); err != nil {
		return err
	}
	metadata, origin, history, err := encodeDeadLetterJSON(d)
	if err != nil {
		return err
	}
	var settledAt any
	if d.SettledAt != nil {
		settledAt = d.SettledAt.UTC()
	}
	return s.execWithRetry(ctx, func() error {
		_, err := s.exec(ctx, s.queries.get(QueryInsertDeadLetter),
			d.ID, d.VHost, d.WorkflowID, d.MessageID, d.NodeID, d.SinkID, d.Error, d.FailedAt,
			d.Payload, d.PayloadSize, d.Truncated, metadata, origin, string(d.Status), settledAt, d.SettledBy, history)
		if err != nil {
			return fmt.Errorf("keeping dead letter %s: %w", d.ID, err)
		}
		return nil
	})
}

func encodeDeadLetterJSON(d storage.DeadLetter) (metadata, origin, history string, err error) {
	enc := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		return string(raw), err
	}
	if metadata, err = enc(d.Metadata); err != nil {
		return "", "", "", fmt.Errorf("encoding the metadata of dead letter %s: %w", d.ID, err)
	}
	if origin, err = enc(d.Origin); err != nil {
		return "", "", "", fmt.Errorf("encoding the origin of dead letter %s: %w", d.ID, err)
	}
	if history, err = enc(d.History); err != nil {
		return "", "", "", fmt.Errorf("encoding the history of dead letter %s: %w", d.ID, err)
	}
	return metadata, origin, history, nil
}

func (s *sqlStorage) GetDeadLetter(ctx context.Context, id string) (storage.DeadLetter, error) {
	var d storage.DeadLetter
	var msgID, nodeID, sinkID, errText, metadata, origin, status, settledBy, history sql.NullString
	var failedAt, settledAt any
	var truncated sql.NullBool
	err := s.queryRow(ctx, s.queries.get(QueryGetDeadLetter), id).Scan(
		&d.ID, &d.VHost, &d.WorkflowID, &msgID, &nodeID, &sinkID, &errText, &failedAt,
		&d.Payload, &d.PayloadSize, &truncated, &metadata, &origin, &status, &settledAt, &settledBy, &history)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.DeadLetter{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.DeadLetter{}, fmt.Errorf("reading dead letter %s: %w", id, err)
	}
	d.MessageID, d.NodeID, d.SinkID, d.Error = msgID.String, nodeID.String, sinkID.String, errText.String
	d.FailedAt = coerceTime(failedAt).UTC()
	d.Truncated = truncated.Bool
	d.Status = storage.DeadLetterStatus(status.String)
	d.SettledBy = settledBy.String
	if t := coerceTime(settledAt); !t.IsZero() {
		t = t.UTC()
		d.SettledAt = &t
	}
	for _, f := range []struct {
		raw  sql.NullString
		into any
		name string
	}{{metadata, &d.Metadata, "metadata"}, {origin, &d.Origin, "origin"}, {history, &d.History, "history"}} {
		if f.raw.String == "" || f.raw.String == "null" {
			continue
		}
		if err := json.Unmarshal([]byte(f.raw.String), f.into); err != nil {
			return storage.DeadLetter{}, fmt.Errorf("the %s of dead letter %s is unreadable: %w", f.name, id, err)
		}
	}
	return d, nil
}

// deadLetterWhere is the WHERE clause, with its arguments, that a filter and
// the stats share.
func deadLetterWhere(f storage.DeadLetterFilter) (string, []any) {
	var where []string
	var args []any
	if vhosts := storage.WithSharedVHosts(f.VHosts); vhosts != nil {
		where = append(where, "vhost IN ("+strings.TrimSuffix(strings.Repeat("?, ", len(vhosts)), ", ")+")")
		for _, v := range vhosts {
			args = append(args, v)
		}
	}
	if f.WorkflowID != "" {
		where = append(where, "workflow_id = ?")
		args = append(args, f.WorkflowID)
	}
	if f.Where != "" {
		where = append(where, "(node_id = ? OR sink_id = ?)")
		args = append(args, f.Where, f.Where)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Search != "" {
		clause, searchArgs := caseInsensitiveSearch(f.Search, "error", "message_id", "node_id", "sink_id")
		where = append(where, clause)
		args = append(args, searchArgs...)
	}
	if !f.Since.IsZero() {
		where = append(where, "failed_at >= ?")
		args = append(args, f.Since.UTC())
	}
	if !f.Until.IsZero() {
		where = append(where, "failed_at <= ?")
		args = append(args, f.Until.UTC())
	}
	if len(where) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

// pageClause is LIMIT and OFFSET in the current dialect, with its arguments.
// SQL Server has no LIMIT and takes the offset first.
func (s *sqlStorage) pageClause(limit, offset int) (string, []any) {
	if s.driver == "sqlserver" {
		return " OFFSET ? ROWS FETCH NEXT ? ROWS ONLY", []any{offset, limit}
	}
	return " LIMIT ? OFFSET ?", []any{limit, offset}
}

func (s *sqlStorage) ListDeadLetters(ctx context.Context, f storage.DeadLetterFilter) ([]storage.DeadLetter, int, error) {
	where, args := deadLetterWhere(f)
	var total int
	if err := s.queryRow(ctx, s.queries.get(QueryCountDeadLetters)+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting dead letters: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	page, pageArgs := s.pageClause(limit, max(f.Offset, 0))
	rows, err := s.query(ctx, s.queries.get(QueryListDeadLetters)+where+" ORDER BY failed_at DESC, id DESC"+page,
		append(args, pageArgs...)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing dead letters: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []storage.DeadLetter{}
	for rows.Next() {
		var d storage.DeadLetter
		var msgID, nodeID, sinkID, errText, status, settledBy sql.NullString
		var failedAt, settledAt any
		var truncated sql.NullBool
		if err := rows.Scan(&d.ID, &d.VHost, &d.WorkflowID, &msgID, &nodeID, &sinkID, &errText, &failedAt,
			&d.PayloadSize, &truncated, &status, &settledAt, &settledBy); err != nil {
			return nil, 0, fmt.Errorf("listing dead letters: %w", err)
		}
		d.MessageID, d.NodeID, d.SinkID, d.Error = msgID.String, nodeID.String, sinkID.String, errText.String
		d.FailedAt = coerceTime(failedAt).UTC()
		d.Truncated = truncated.Bool
		d.Status = storage.DeadLetterStatus(status.String)
		d.SettledBy = settledBy.String
		if t := coerceTime(settledAt); !t.IsZero() {
			t = t.UTC()
			d.SettledAt = &t
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("listing dead letters: %w", err)
	}
	return out, total, nil
}

// SettleDeadLetter reads the history, appends ev and writes it back with an
// UPDATE that only matches a queued row, so of two concurrent settles exactly
// one changes the row and the other is told it was too late.
func (s *sqlStorage) SettleDeadLetter(ctx context.Context, id string, status storage.DeadLetterStatus, ev storage.DeadLetterEvent) error {
	if !storage.ValidSettledStatus(status) {
		return fmt.Errorf("a dead letter cannot be settled as %q", status)
	}
	d, err := s.GetDeadLetter(ctx, id)
	if err != nil {
		return err
	}
	if d.Status != storage.DeadLetterQueued {
		return storage.ErrDeadLetterSettled
	}
	raw, err := json.Marshal(storage.AppendDeadLetterEvent(d.History, ev))
	if err != nil {
		return fmt.Errorf("encoding the history of dead letter %s: %w", id, err)
	}
	var n int64
	err = s.execWithRetry(ctx, func() error {
		res, err := s.exec(ctx, s.queries.get(QuerySettleDeadLetter), string(status), ev.At.UTC(), ev.By, string(raw), id)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return fmt.Errorf("settling dead letter %s: %w", id, err)
	}
	if n == 0 {
		return storage.ErrDeadLetterSettled
	}
	return nil
}

// AddDeadLetterEvent appends ev to the history. The UPDATE requires the status
// it read, so an event cannot land on a row that was settled in between and
// overwrite the settle's own event.
func (s *sqlStorage) AddDeadLetterEvent(ctx context.Context, id string, ev storage.DeadLetterEvent) error {
	d, err := s.GetDeadLetter(ctx, id)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(storage.AppendDeadLetterEvent(d.History, ev))
	if err != nil {
		return fmt.Errorf("encoding the history of dead letter %s: %w", id, err)
	}
	var n int64
	err = s.execWithRetry(ctx, func() error {
		res, err := s.exec(ctx, s.queries.get(QuerySetDeadLetterHistory), string(raw), id, string(d.Status))
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return fmt.Errorf("noting an event on dead letter %s: %w", id, err)
	}
	if n == 0 {
		return storage.ErrDeadLetterSettled
	}
	return nil
}

func (s *sqlStorage) DeadLetterStats(ctx context.Context, vhosts []string, now time.Time) (storage.DeadLetterStats, error) {
	var st storage.DeadLetterStats
	count := func(f storage.DeadLetterFilter, extra string, extraArgs ...any) (int, error) {
		where, args := deadLetterWhere(f)
		if extra != "" {
			if where == "" {
				where = " WHERE " + extra
			} else {
				where += " AND " + extra
			}
			args = append(args, extraArgs...)
		}
		var n int
		err := s.queryRow(ctx, s.queries.get(QueryCountDeadLetters)+where, args...).Scan(&n)
		return n, err
	}
	var err error
	queued := storage.DeadLetterFilter{VHosts: vhosts, Status: storage.DeadLetterQueued}
	if st.Queued, err = count(queued, ""); err != nil {
		return st, fmt.Errorf("counting queued dead letters: %w", err)
	}
	if st.Last24h, err = count(storage.DeadLetterFilter{VHosts: vhosts, Since: now.Add(-24 * time.Hour)}, ""); err != nil {
		return st, fmt.Errorf("counting recent dead letters: %w", err)
	}
	if st.ReplayedThisWeek, err = count(storage.DeadLetterFilter{VHosts: vhosts, Status: storage.DeadLetterReplayed},
		"settled_at >= ?", now.Add(-7*24*time.Hour).UTC()); err != nil {
		return st, fmt.Errorf("counting replayed dead letters: %w", err)
	}
	where, args := deadLetterWhere(queued)
	var topError sql.NullString
	err = s.queryRow(ctx, fmt.Sprintf(s.queries.get(QueryDeadLetterTopError), where), args...).Scan(&topError, &st.TopErrorCount)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, fmt.Errorf("finding the most common dead letter error: %w", err)
	}
	st.TopError = topError.String
	return st, nil
}

func (s *sqlStorage) PurgeDeadLetters(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.exec(ctx, s.queries.get(QueryPurgeDeadLetters), cutoff.UTC())
	if err != nil {
		return 0, fmt.Errorf("purging dead letters: %w", err)
	}
	return res.RowsAffected()
}
