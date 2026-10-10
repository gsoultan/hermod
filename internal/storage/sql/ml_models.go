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
	// FeatureTypes and MCPExposed live in spec too: a new field on a model
	// is not a schema change.
	FeatureTypes map[string]string `json:"feature_types,omitempty"`
	MCPExposed   bool              `json:"mcp_exposed,omitempty"`
}

func specOf(m storage.MLModel) mlModelSpec {
	return mlModelSpec{
		Description: m.Description, Backend: string(m.Backend), URL: m.URL,
		RemoteModel: m.RemoteModel, RemoteVersion: m.RemoteVersion, TokenSecret: m.TokenSecret,
		InputName: m.InputName, Features: m.Features, TimeoutMs: m.TimeoutMs,
		FeatureTypes: m.FeatureTypes, MCPExposed: m.MCPExposed,
	}
}

func (sp mlModelSpec) apply(m *storage.MLModel) {
	m.Description, m.Backend, m.URL = sp.Description, inference.Backend(sp.Backend), sp.URL
	m.RemoteModel, m.RemoteVersion, m.TokenSecret = sp.RemoteModel, sp.RemoteVersion, sp.TokenSecret
	m.InputName, m.Features, m.TimeoutMs = sp.InputName, sp.Features, sp.TimeoutMs
	m.FeatureTypes, m.MCPExposed = sp.FeatureTypes, sp.MCPExposed
}

// scanMLModel fills m from a row's spec and bookkeeping columns.
// mlModelRow is what a query reads after the keys: the definition, the
// bookkeeping, and the retrain policy and status.
type mlModelRow struct {
	spec, hash, by, retrain, status sql.NullString
	created, updated                sql.NullTime
}

func (r *mlModelRow) targets() []any {
	return []any{&r.spec, &r.hash, &r.by, &r.created, &r.updated, &r.retrain, &r.status}
}

// fill fills m from the row.
func (r *mlModelRow) fill(m *storage.MLModel) error {
	var sp mlModelSpec
	if r.spec.String != "" {
		if err := json.Unmarshal([]byte(r.spec.String), &sp); err != nil {
			return fmt.Errorf("model %q of vhost %q has an unreadable definition: %w", m.Name, m.VHost, err)
		}
	}
	sp.apply(m)
	m.ServingKeyHash, m.Serving = r.hash.String, r.hash.String != ""
	m.UpdatedBy, m.CreatedAt, m.UpdatedAt = r.by.String, r.created.Time, r.updated.Time
	if r.retrain.String != "" {
		m.Retrain = &storage.MLRetrainPolicy{}
		if err := json.Unmarshal([]byte(r.retrain.String), m.Retrain); err != nil {
			return fmt.Errorf("model %q of vhost %q has an unreadable retrain policy: %w", m.Name, m.VHost, err)
		}
	}
	if r.status.String != "" {
		m.RetrainStatus = &storage.MLRetrainStatus{}
		if err := json.Unmarshal([]byte(r.status.String), m.RetrainStatus); err != nil {
			return fmt.Errorf("model %q of vhost %q has an unreadable retrain status: %w", m.Name, m.VHost, err)
		}
	}
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
		var r mlModelRow
		if err := rows.Scan(append([]any{&m.Name}, r.targets()...)...); err != nil {
			return nil, fmt.Errorf("listing models of vhost %q: %w", vhost, err)
		}
		if err := r.fill(&m); err != nil {
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
	var r mlModelRow
	err := s.queryRow(ctx, s.queries.get(QueryGetMLModel), mlModelID(vhost, name)).Scan(r.targets()...)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.MLModel{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLModel{}, fmt.Errorf("reading model %q of vhost %q: %w", name, vhost, err)
	}
	if err := r.fill(&m); err != nil {
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
