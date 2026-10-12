package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// mlScheduleID is the row key: vhost + "/" + kind + "/" + name. A vhost name
// cannot hold a slash, and kind is one of two words, so the key is unique.
func mlScheduleID(vhost, kind, name string) string {
	return vhost + "/" + kind + "/" + name
}

func (s *sqlStorage) ListMLSchedules(ctx context.Context) ([]storage.MLSchedule, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLSchedules))
	if err != nil {
		return nil, fmt.Errorf("listing ML schedules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []storage.MLSchedule
	for rows.Next() {
		sc, err := scanMLSchedule(rows)
		if err != nil {
			return nil, fmt.Errorf("listing ML schedules: %w", err)
		}
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing ML schedules: %w", err)
	}
	return out, nil
}

func (s *sqlStorage) GetMLSchedule(ctx context.Context, vhost, kind, name string) (storage.MLSchedule, error) {
	sc, err := scanMLSchedule(s.queryRow(ctx, s.queries.get(QueryGetMLSchedule), mlScheduleID(vhost, kind, name)))
	if errors.Is(err, sql.ErrNoRows) {
		return storage.MLSchedule{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLSchedule{}, fmt.Errorf("reading the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return sc, nil
}

// scanMLSchedule reads the columns QueryListMLSchedules and QueryGetMLSchedule
// select, in their order.
func scanMLSchedule(row interface{ Scan(...any) error }) (storage.MLSchedule, error) {
	var sc storage.MLSchedule
	var spec, lastErr, by sql.NullString
	var lastRun, updated sql.NullTime
	var lastRows sql.NullInt64
	if err := row.Scan(&sc.VHost, &sc.Kind, &sc.Name, &sc.Schedule, &spec, &lastRun, &lastRows, &lastErr, &by, &updated); err != nil {
		return sc, err
	}
	if spec.String != "" {
		sc.Spec = []byte(spec.String)
	}
	sc.LastRun, sc.LastRows, sc.LastError = lastRun.Time, int(lastRows.Int64), lastErr.String
	sc.UpdatedBy, sc.UpdatedAt = by.String, updated.Time
	return sc, nil
}

// PutMLSchedule updates the row and inserts it when there is none, as
// PutMLQuotas does; the last run's columns are left as they are.
func (s *sqlStorage) PutMLSchedule(ctx context.Context, sc storage.MLSchedule) error {
	if err := storage.ValidateMLSchedule(sc); err != nil {
		return err
	}
	id := mlScheduleID(sc.VHost, sc.Kind, sc.Name)
	now := time.Now().UTC()
	update := func() (bool, error) {
		res, err := s.exec(ctx, s.queries.get(QueryUpdateMLSchedule), sc.Schedule, string(sc.Spec), sc.UpdatedBy, now, id)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		return n > 0, err
	}
	updated, err := update()
	if err != nil {
		return fmt.Errorf("saving the %s schedule of %q in vhost %q: %w", sc.Kind, sc.Name, sc.VHost, err)
	}
	if updated {
		return nil
	}
	if _, err := s.exec(ctx, s.queries.get(QueryInsertMLSchedule), id, sc.VHost, sc.Kind, sc.Name, sc.Schedule,
		string(sc.Spec), sc.UpdatedBy, now); err != nil {
		// Another writer inserted the row first: update it instead.
		if updated, retryErr := update(); retryErr == nil && updated {
			return nil
		}
		return fmt.Errorf("saving the %s schedule of %q in vhost %q: %w", sc.Kind, sc.Name, sc.VHost, err)
	}
	return nil
}

func (s *sqlStorage) DeleteMLSchedule(ctx context.Context, vhost, kind, name string) error {
	res, err := s.exec(ctx, s.queries.get(QueryDeleteMLSchedule), mlScheduleID(vhost, kind, name))
	if err != nil {
		return fmt.Errorf("deleting the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) SetMLScheduleRun(ctx context.Context, vhost, kind, name string, at time.Time, rows int, runErr string) error {
	if _, err := s.exec(ctx, s.queries.get(QuerySetMLScheduleRun), at.UTC(), rows, runErr, mlScheduleID(vhost, kind, name)); err != nil {
		return fmt.Errorf("recording the run of the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return nil
}

// ClaimMLSchedule is ClaimMLModelTraining on a schedule's row.
func (s *sqlStorage) ClaimMLSchedule(ctx context.Context, vhost, kind, name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	res, err := s.exec(ctx, s.queries.get(QueryClaimMLSchedule), owner, now.Add(ttl), mlScheduleID(vhost, kind, name), now, owner)
	if err != nil {
		return false, fmt.Errorf("claiming the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0, nil
}

func (s *sqlStorage) ReleaseMLSchedule(ctx context.Context, vhost, kind, name, owner string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryReleaseMLSchedule), mlScheduleID(vhost, kind, name), owner); err != nil {
		return fmt.Errorf("releasing the %s schedule of %q in vhost %q: %w", kind, name, vhost, err)
	}
	return nil
}

func (s *sqlStorage) DeleteMLSchedules(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLSchedulesOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the ML schedules of vhost %q: %w", vhost, err)
	}
	return nil
}

// AddMLFeedback inserts every event in one transaction.
func (s *sqlStorage) AddMLFeedback(ctx context.Context, events []storage.MLFeedbackEvent) error {
	for _, e := range events {
		if err := storage.ValidateMLFeedbackEvent(e); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("saving feedback: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.prepareQuery(s.queries.get(QueryInsertMLFeedback))
	for _, e := range events {
		if _, err := tx.ExecContext(ctx, q, e.VHost, e.Model, e.RecommendationID, e.ItemID, e.Outcome, e.At.UTC()); err != nil {
			return fmt.Errorf("saving feedback on model %q of vhost %q: %w", e.Model, e.VHost, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("saving feedback: %w", err)
	}
	return nil
}

func (s *sqlStorage) CountMLFeedback(ctx context.Context, vhost, model string, since time.Time) (map[string]int, error) {
	rows, err := s.query(ctx, s.queries.get(QueryCountMLFeedback), vhost, model, since.UTC())
	if err != nil {
		return nil, fmt.Errorf("counting feedback on model %q of vhost %q: %w", model, vhost, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int{}
	for rows.Next() {
		var outcome string
		var n int
		if err := rows.Scan(&outcome, &n); err != nil {
			return nil, fmt.Errorf("counting feedback on model %q of vhost %q: %w", model, vhost, err)
		}
		out[outcome] = n
	}
	return out, rows.Err()
}

func (s *sqlStorage) DeleteMLFeedback(ctx context.Context, vhost, model string) error {
	var err error
	if model == "" {
		_, err = s.exec(ctx, s.queries.get(QueryDeleteMLFeedbackOfVHost), vhost)
	} else {
		_, err = s.exec(ctx, s.queries.get(QueryDeleteMLFeedbackOfModel), vhost, model)
	}
	if err != nil {
		return fmt.Errorf("deleting feedback of vhost %q: %w", vhost, err)
	}
	return nil
}
