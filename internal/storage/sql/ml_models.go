package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/ml/inference"
)

// mlModelID is the row key: vhost + "/" + name. A model name cannot hold a
// slash (storage.ValidMLModelName), so the last one always separates the two.
func mlModelID(vhost, name string) string {
	return vhost + "/" + name
}

// mlModelSpec is what the spec column holds: everything that defines the model
// except the keys and bookkeeping that have columns of their own.
type mlModelSpec struct {
	Description   string   `json:"description,omitempty"`
	Backend       string   `json:"backend"`
	URL           string   `json:"url"`
	RemoteModel   string   `json:"remote_model,omitempty"`
	RemoteVersion string   `json:"remote_version,omitempty"`
	TokenSecret   string   `json:"token_secret,omitempty"`
	InputName     string   `json:"input_name,omitempty"`
	Features      []string `json:"features,omitempty"`
	TimeoutMs     int      `json:"timeout_ms,omitempty"`

	Monitoring storage.MLMonitoring `json:"monitoring"`
}

func specOf(m storage.MLModel) mlModelSpec {
	return mlModelSpec{
		Description: m.Description, Backend: string(m.Backend), URL: m.URL,
		RemoteModel: m.RemoteModel, RemoteVersion: m.RemoteVersion, TokenSecret: m.TokenSecret,
		InputName: m.InputName, Features: m.Features, TimeoutMs: m.TimeoutMs, Monitoring: m.Monitoring,
	}
}

func (sp mlModelSpec) apply(m *storage.MLModel) {
	m.Description, m.Backend, m.URL = sp.Description, inference.Backend(sp.Backend), sp.URL
	m.RemoteModel, m.RemoteVersion, m.TokenSecret = sp.RemoteModel, sp.RemoteVersion, sp.TokenSecret
	m.InputName, m.Features, m.TimeoutMs = sp.InputName, sp.Features, sp.TimeoutMs
	m.Monitoring = sp.Monitoring
}

// scanMLModel fills m from a row's spec and bookkeeping columns.
func scanMLModel(m *storage.MLModel, spec, hash, by sql.NullString, created, updated sql.NullTime) error {
	var sp mlModelSpec
	if spec.String != "" {
		if err := json.Unmarshal([]byte(spec.String), &sp); err != nil {
			return fmt.Errorf("model %q of vhost %q has an unreadable definition: %w", m.Name, m.VHost, err)
		}
	}
	sp.apply(m)
	m.ServingKeyHash, m.Serving = hash.String, hash.String != ""
	m.UpdatedBy, m.CreatedAt, m.UpdatedAt = by.String, created.Time, updated.Time
	return nil
}

func (s *sqlStorage) ListMLModels(ctx context.Context, vhost string) ([]storage.MLModel, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLModels), vhost)
	if err != nil {
		return nil, fmt.Errorf("listing models of vhost %q: %w", vhost, err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.MLModel
	for rows.Next() {
		m := storage.MLModel{VHost: vhost}
		var spec, hash, by sql.NullString
		var created, updated sql.NullTime
		if err := rows.Scan(&m.Name, &spec, &hash, &by, &created, &updated); err != nil {
			return nil, fmt.Errorf("listing models of vhost %q: %w", vhost, err)
		}
		if err := scanMLModel(&m, spec, hash, by, created, updated); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing models of vhost %q: %w", vhost, err)
	}
	return out, nil
}

func (s *sqlStorage) GetMLModel(ctx context.Context, vhost, name string) (storage.MLModel, error) {
	m := storage.MLModel{VHost: vhost, Name: name}
	var spec, hash, by sql.NullString
	var created, updated sql.NullTime
	err := s.queryRow(ctx, s.queries.get(QueryGetMLModel), mlModelID(vhost, name)).
		Scan(&spec, &hash, &by, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.MLModel{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLModel{}, fmt.Errorf("reading model %q of vhost %q: %w", name, vhost, err)
	}
	if err := scanMLModel(&m, spec, hash, by, created, updated); err != nil {
		return storage.MLModel{}, err
	}
	return m, nil
}

// PutMLModel updates the row if the vhost already holds the name and inserts
// it otherwise, the same update-then-insert PutVHostSecret uses and for the
// same reason: the dialects here spell an upsert three ways.
func (s *sqlStorage) PutMLModel(ctx context.Context, m storage.MLModel) error {
	if err := storage.ValidateMLModel(m); err != nil {
		return err
	}
	spec, err := json.Marshal(specOf(m))
	if err != nil {
		return fmt.Errorf("encoding model %q: %w", m.Name, err)
	}
	id := mlModelID(m.VHost, m.Name)
	now := time.Now().UTC()

	updated, err := s.updateMLModel(ctx, id, string(spec), m.UpdatedBy, now)
	if err != nil || updated {
		return err
	}
	if _, err := s.exec(ctx, s.queries.get(QueryInsertMLModel),
		id, m.VHost, m.Name, string(spec), "", m.UpdatedBy, now, now); err != nil {
		if updated, retryErr := s.updateMLModel(ctx, id, string(spec), m.UpdatedBy, now); retryErr == nil && updated {
			return nil
		}
		return fmt.Errorf("saving model %q of vhost %q: %w", m.Name, m.VHost, err)
	}
	return nil
}

func (s *sqlStorage) updateMLModel(ctx context.Context, id, spec, by string, at time.Time) (bool, error) {
	res, err := s.exec(ctx, s.queries.get(QueryUpdateMLModel), spec, by, at, id)
	if err != nil {
		return false, fmt.Errorf("saving model: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("saving model: %w", err)
	}
	return n > 0, nil
}

func (s *sqlStorage) SetMLModelServingKey(ctx context.Context, vhost, name, hash string) error {
	res, err := s.exec(ctx, s.queries.get(QuerySetMLModelServingKey), hash, mlModelID(vhost, name))
	if err != nil {
		return fmt.Errorf("setting the serving key of model %q of vhost %q: %w", name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) DeleteMLModel(ctx context.Context, vhost, name string) error {
	res, err := s.exec(ctx, s.queries.get(QueryDeleteMLModel), mlModelID(vhost, name))
	if err != nil {
		return fmt.Errorf("deleting model %q of vhost %q: %w", name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) DeleteMLModels(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLModelsOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the models of vhost %q: %w", vhost, err)
	}
	return nil
}

// InsertMLPredictionLogs writes the batch in one transaction, through one
// prepared statement, as CreateLogs does.
func (s *sqlStorage) InsertMLPredictionLogs(ctx context.Context, logs []storage.MLPredictionLog) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("writing prediction logs: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, s.prepareQuery(s.queries.get(QueryInsertMLPredictionLog)))
	if err != nil {
		return fmt.Errorf("writing prediction logs: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, l := range logs {
		inputs, err := json.Marshal(l.Inputs)
		if err != nil {
			return fmt.Errorf("encoding a logged prediction of model %q: %w", l.Model, err)
		}
		outputs, err := json.Marshal(l.Outputs)
		if err != nil {
			return fmt.Errorf("encoding a logged prediction of model %q: %w", l.Model, err)
		}
		if _, err := stmt.ExecContext(ctx, l.VHost, l.Model, l.Version, l.Timestamp.UTC(),
			string(inputs), string(outputs), l.LatencyMs, l.CallerKind, l.CallerID); err != nil {
			return fmt.Errorf("writing prediction logs: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("writing prediction logs: %w", err)
	}
	return nil
}

func (s *sqlStorage) ListMLPredictionLogs(ctx context.Context, vhost, model string, limit int) ([]storage.MLPredictionLog, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLPredictionLogs), vhost, model, storage.MLPredictionLogLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("reading the prediction log of model %q of vhost %q: %w", model, vhost, err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.MLPredictionLog
	for rows.Next() {
		l := storage.MLPredictionLog{VHost: vhost, Model: model}
		var version, inputs, outputs, kind, id sql.NullString
		if err := rows.Scan(&version, &l.Timestamp, &inputs, &outputs, &l.LatencyMs, &kind, &id); err != nil {
			return nil, fmt.Errorf("reading the prediction log of model %q of vhost %q: %w", model, vhost, err)
		}
		l.Version, l.CallerKind, l.CallerID = version.String, kind.String, id.String
		if err := decodeLogged(inputs, &l.Inputs); err != nil {
			return nil, err
		}
		if err := decodeLogged(outputs, &l.Outputs); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the prediction log of model %q of vhost %q: %w", model, vhost, err)
	}
	return out, nil
}

func decodeLogged(raw sql.NullString, into *map[string]any) error {
	if raw.String == "" || raw.String == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw.String), into); err != nil {
		return fmt.Errorf("a logged prediction is unreadable: %w", err)
	}
	return nil
}

func (s *sqlStorage) PurgeMLPredictionLogs(ctx context.Context, vhost, model string, before time.Time) error {
	var err error
	switch {
	case vhost == "":
		_, err = s.exec(ctx, s.queries.get(QueryPurgeMLPredictionLogs), before.UTC())
	case model == "":
		_, err = s.exec(ctx, s.queries.get(QueryPurgeMLPredictionLogsOfVHost), vhost, before.UTC())
	default:
		_, err = s.exec(ctx, s.queries.get(QueryPurgeMLPredictionLogsOfModel), vhost, model, before.UTC())
	}
	if err != nil {
		return fmt.Errorf("purging prediction logs: %w", err)
	}
	return nil
}

func (s *sqlStorage) DeleteMLPredictionLogs(ctx context.Context, vhost, model string) error {
	var err error
	if model == "" {
		_, err = s.exec(ctx, s.queries.get(QueryDeleteMLPredictionLogsOfVHost), vhost)
	} else {
		_, err = s.exec(ctx, s.queries.get(QueryDeleteMLPredictionLogsOfModel), vhost, model)
	}
	if err != nil {
		return fmt.Errorf("deleting the prediction logs of vhost %q: %w", vhost, err)
	}
	return nil
}
