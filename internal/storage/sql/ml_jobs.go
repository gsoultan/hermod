// Copyright (c) 2025 Gembit Soultan Shirazi. All rights reserved.
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
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// mlJobRow is the nullable columns of an ml_jobs row, in the order the job
// queries select them.
type mlJobRow struct {
	progressMsg, request, result, resultRef, errText     sql.NullString
	callbackURL, callbackStatus, callbackErr, callerKind sql.NullString
	createdBy, claimedBy                                 sql.NullString
	callbackAttempts, attempts                           sql.NullInt64
	cancel                                               sql.NullBool
	callbackNext, claimUntil, started, finished          sql.NullTime
}

func (r *mlJobRow) targets(j *storage.MLJob) []any {
	return []any{&j.ID, &j.VHost, &j.Model, &j.Kind, &j.Status, &j.Progress, &r.progressMsg, &r.request, &r.result,
		&r.resultRef, &r.errText, &r.callbackURL, &r.callbackStatus, &r.callbackAttempts, &r.callbackNext, &r.callbackErr,
		&r.callerKind, &r.createdBy, &r.cancel, &r.attempts, &r.claimedBy, &r.claimUntil, &j.CreatedAt, &r.started,
		&r.finished, &j.ExpiresAt}
}

func (r *mlJobRow) fill(j *storage.MLJob) {
	j.ProgressMessage, j.ResultRef, j.Error = r.progressMsg.String, r.resultRef.String, r.errText.String
	if r.request.Valid && r.request.String != "" {
		j.Request = json.RawMessage(r.request.String)
	}
	if r.result.Valid && r.result.String != "" {
		j.Result = json.RawMessage(r.result.String)
	}
	j.CallbackURL, j.CallbackStatus, j.CallbackLastError = r.callbackURL.String, r.callbackStatus.String, r.callbackErr.String
	j.CallbackAttempts, j.CallbackNextAt = int(r.callbackAttempts.Int64), r.callbackNext.Time
	j.CallerKind, j.CreatedBy, j.CancelRequested = r.callerKind.String, r.createdBy.String, r.cancel.Bool
	j.Attempts, j.ClaimedBy, j.ClaimUntil = int(r.attempts.Int64), r.claimedBy.String, r.claimUntil.Time
	if r.started.Valid {
		t := r.started.Time
		j.StartedAt = &t
	}
	if r.finished.Valid {
		t := r.finished.Time
		j.FinishedAt = &t
	}
}

// nullable is NULL for an empty string, so an absent value reads back the
// same on every engine.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableTime is NULL for the zero time.
func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}

func (s *sqlStorage) CreateMLJob(ctx context.Context, j storage.MLJob) error {
	_, err := s.exec(ctx, s.queries.get(QueryInsertMLJob),
		j.ID, j.VHost, j.Model, j.Kind, j.Status, j.Progress, nullable(j.ProgressMessage), nullable(string(j.Request)),
		nullable(string(j.Result)), nullable(j.ResultRef), nullable(j.Error), nullable(j.CallbackURL),
		nullable(j.CallbackStatus), j.CallbackAttempts, nullableTime(&j.CallbackNextAt), nullable(j.CallbackLastError),
		nullable(j.CallerKind), nullable(j.CreatedBy), j.CancelRequested, j.Attempts, nullable(j.ClaimedBy),
		nullableTime(&j.ClaimUntil), j.CreatedAt.UTC(), nullableTime(j.StartedAt), nullableTime(j.FinishedAt), j.ExpiresAt.UTC())
	if err != nil {
		return fmt.Errorf("saving ML job %q of vhost %q: %w", j.ID, j.VHost, err)
	}
	return nil
}

func (s *sqlStorage) GetMLJob(ctx context.Context, vhost, id string) (storage.MLJob, error) {
	var j storage.MLJob
	var r mlJobRow
	err := s.queryRow(ctx, s.queries.get(QueryGetMLJob), vhost, id).Scan(r.targets(&j)...)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.MLJob{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLJob{}, fmt.Errorf("reading ML job %q of vhost %q: %w", id, vhost, err)
	}
	r.fill(&j)
	return j, nil
}

func (s *sqlStorage) ListMLJobs(ctx context.Context, f storage.MLJobFilter) ([]storage.MLJob, int, error) {
	limit := f.Limit
	if limit <= 0 || limit > storage.MaxMLJobPage {
		limit = storage.MaxMLJobPage
	}
	offset := max(f.Offset, 0)
	filter := []any{f.VHost, f.Model, f.Model, f.Status, f.Status}
	page := []any{limit, offset}
	if s.driver == "sqlserver" {
		page = []any{offset, limit}
	}
	var total int
	if err := s.queryRow(ctx, s.queries.get(QueryCountMLJobs), filter...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting the ML jobs of vhost %q: %w", f.VHost, err)
	}
	rows, err := s.query(ctx, s.queries.get(QueryListMLJobs), append(filter, page...)...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing the ML jobs of vhost %q: %w", f.VHost, err)
	}
	defer func() { _ = rows.Close() }()
	out := []storage.MLJob{}
	for rows.Next() {
		var j storage.MLJob
		var r mlJobRow
		if err := rows.Scan(r.targets(&j)...); err != nil {
			return nil, 0, fmt.Errorf("listing the ML jobs of vhost %q: %w", f.VHost, err)
		}
		r.fill(&j)
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("listing the ML jobs of vhost %q: %w", f.VHost, err)
	}
	return out, total, nil
}

// candidates reads the ids a claim may try, oldest first.
func (s *sqlStorage) candidates(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.query(ctx, s.queries.get(query), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// getMLJobByID reads a job a claim just took; the claim knows its id, not
// its vhost.
func (s *sqlStorage) getMLJobByID(ctx context.Context, id string) (storage.MLJob, error) {
	var vhost string
	if err := s.queryRow(ctx, s.queries.get(QueryGetMLJobVHost), id).Scan(&vhost); err != nil {
		return storage.MLJob{}, err
	}
	return s.GetMLJob(ctx, vhost, id)
}

// ClaimMLJob is ClaimMLModelTraining on a job: of every Hermod updating one
// candidate with the same condition, one update takes a row.
func (s *sqlStorage) ClaimMLJob(ctx context.Context, owner string, ttl time.Duration, now time.Time) (storage.MLJob, bool, error) {
	now = now.UTC()
	ids, err := s.candidates(ctx, QueryMLJobClaimCandidates, now)
	if err != nil {
		return storage.MLJob{}, false, fmt.Errorf("finding an ML job to run: %w", err)
	}
	for _, id := range ids {
		res, err := s.exec(ctx, s.queries.get(QueryClaimMLJob), owner, now.Add(ttl), now, id, now)
		if err != nil {
			return storage.MLJob{}, false, fmt.Errorf("claiming ML job %q: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			continue
		}
		j, err := s.getMLJobByID(ctx, id)
		if err != nil {
			return storage.MLJob{}, false, fmt.Errorf("reading claimed ML job %q: %w", id, err)
		}
		return j, true, nil
	}
	return storage.MLJob{}, false, nil
}

func (s *sqlStorage) UpdateMLJobProgress(ctx context.Context, id, owner string, progress float64, message string, until time.Time) (bool, error) {
	res, err := s.exec(ctx, s.queries.get(QueryUpdateMLJobProgress), progress, nullable(message), until.UTC(), id, owner)
	if err != nil {
		return false, fmt.Errorf("recording the progress of ML job %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return false, storage.ErrMLJobClaimLost
	}
	var cancel sql.NullBool
	if err := s.queryRow(ctx, s.queries.get(QueryMLJobCancelRequested), id).Scan(&cancel); err != nil {
		return false, fmt.Errorf("reading whether ML job %q is cancelled: %w", id, err)
	}
	return cancel.Bool, nil
}

func (s *sqlStorage) FinishMLJob(ctx context.Context, id, owner string, f storage.MLJobFinish) error {
	// The callback is due at once; a job without a callback URL keeps none.
	var callbackURL sql.NullString
	if err := s.queryRow(ctx, s.queries.get(QueryGetMLJobCallbackURL), id).Scan(&callbackURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return storage.ErrMLJobClaimLost
		}
		return fmt.Errorf("finishing ML job %q: %w", id, err)
	}
	var callbackStatus, callbackNext any
	if callbackURL.String != "" {
		callbackStatus, callbackNext = storage.MLCallbackPending, f.At.UTC()
	}
	res, err := s.exec(ctx, s.queries.get(QueryFinishMLJob), f.Status, nullable(string(f.Result)), nullable(f.ResultRef),
		nullable(f.Error), f.At.UTC(), callbackStatus, callbackNext, id, owner)
	if err != nil {
		return fmt.Errorf("finishing ML job %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrMLJobClaimLost
	}
	return nil
}

func (s *sqlStorage) CancelMLJob(ctx context.Context, vhost, id string, now time.Time) (storage.MLJob, error) {
	now = now.UTC()
	if _, err := s.exec(ctx, s.queries.get(QueryCancelQueuedMLJob), now, now, vhost, id); err != nil {
		return storage.MLJob{}, fmt.Errorf("cancelling ML job %q: %w", id, err)
	}
	if _, err := s.exec(ctx, s.queries.get(QueryRequestMLJobCancel), true, vhost, id); err != nil {
		return storage.MLJob{}, fmt.Errorf("cancelling ML job %q: %w", id, err)
	}
	return s.GetMLJob(ctx, vhost, id)
}

func (s *sqlStorage) ClaimMLJobCallback(ctx context.Context, owner string, ttl time.Duration, now time.Time) (storage.MLJob, bool, error) {
	now = now.UTC()
	ids, err := s.candidates(ctx, QueryMLJobCallbackCandidates, now, now)
	if err != nil {
		return storage.MLJob{}, false, fmt.Errorf("finding an ML job callback to send: %w", err)
	}
	for _, id := range ids {
		res, err := s.exec(ctx, s.queries.get(QueryClaimMLJobCallback), owner, now.Add(ttl), id, now, now)
		if err != nil {
			return storage.MLJob{}, false, fmt.Errorf("claiming the callback of ML job %q: %w", id, err)
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			continue
		}
		j, err := s.getMLJobByID(ctx, id)
		if err != nil {
			return storage.MLJob{}, false, fmt.Errorf("reading ML job %q: %w", id, err)
		}
		return j, true, nil
	}
	return storage.MLJob{}, false, nil
}

func (s *sqlStorage) RecordMLJobCallback(ctx context.Context, id, owner, status string, attempts int, next time.Time, lastError string) error {
	res, err := s.exec(ctx, s.queries.get(QueryRecordMLJobCallback), status, attempts, nullableTime(&next), nullable(lastError), id, owner)
	if err != nil {
		return fmt.Errorf("recording the callback of ML job %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrMLJobClaimLost
	}
	return nil
}

// PutMLJobResult replaces any result the job has: a job taken over after a
// crash may have written one before.
func (s *sqlStorage) PutMLJobResult(ctx context.Context, vhost, id string, result json.RawMessage) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLJobResult), vhost, id); err != nil {
		return fmt.Errorf("saving the result of ML job %q: %w", id, err)
	}
	res, err := s.exec(ctx, s.queries.get(QueryInsertMLJobResult), string(result), vhost, id)
	if err != nil {
		return fmt.Errorf("saving the result of ML job %q: %w", id, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) GetMLJobResult(ctx context.Context, vhost, id string) (json.RawMessage, error) {
	var raw sql.NullString
	err := s.queryRow(ctx, s.queries.get(QueryGetMLJobResult), vhost, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading the result of ML job %q: %w", id, err)
	}
	return json.RawMessage(raw.String), nil
}

func (s *sqlStorage) DeleteExpiredMLJobs(ctx context.Context, now time.Time) (int, error) {
	now = now.UTC()
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteExpiredMLResults), now); err != nil {
		return 0, fmt.Errorf("removing expired ML job results: %w", err)
	}
	res, err := s.exec(ctx, s.queries.get(QueryDeleteExpiredMLJobs), now)
	if err != nil {
		return 0, fmt.Errorf("removing expired ML jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *sqlStorage) DeleteMLJobs(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLResultsOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the ML job results of vhost %q: %w", vhost, err)
	}
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLJobsOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the ML jobs of vhost %q: %w", vhost, err)
	}
	return nil
}
