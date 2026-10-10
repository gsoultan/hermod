package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
)

// mlScriptID is the row key: vhost + "/" + name + "/" + version. A script name
// cannot hold a slash (storage.ValidMLModelName).
func mlScriptID(vhost, name string, version int) string {
	return vhost + "/" + name + "/" + strconv.Itoa(version)
}

func (s *sqlStorage) ListMLScripts(ctx context.Context, vhost string) ([]storage.MLScript, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLScripts), vhost)
	if err != nil {
		return nil, fmt.Errorf("listing scripts of vhost %q: %w", vhost, err)
	}
	defer func() { _ = rows.Close() }()

	// Rows come name by name, newest version first; the first of each name
	// is the one to keep.
	var out []storage.MLScript
	for rows.Next() {
		sc := storage.MLScript{VHost: vhost}
		var desc, by sql.NullString
		var created sql.NullTime
		if err := rows.Scan(&sc.Name, &sc.Version, &sc.SHA256, &desc, &by, &created); err != nil {
			return nil, fmt.Errorf("listing scripts of vhost %q: %w", vhost, err)
		}
		if len(out) > 0 && out[len(out)-1].Name == sc.Name {
			continue
		}
		sc.Description, sc.CreatedBy, sc.CreatedAt = desc.String, by.String, created.Time
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing scripts of vhost %q: %w", vhost, err)
	}
	return out, nil
}

func (s *sqlStorage) ListMLScriptVersions(ctx context.Context, vhost, name string) ([]storage.MLScript, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListMLScriptVersions), vhost, name)
	if err != nil {
		return nil, fmt.Errorf("listing versions of script %q of vhost %q: %w", name, vhost, err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.MLScript
	for rows.Next() {
		sc := storage.MLScript{VHost: vhost, Name: name}
		var desc, by sql.NullString
		var created sql.NullTime
		if err := rows.Scan(&sc.Version, &sc.SHA256, &desc, &by, &created); err != nil {
			return nil, fmt.Errorf("listing versions of script %q of vhost %q: %w", name, vhost, err)
		}
		sc.Description, sc.CreatedBy, sc.CreatedAt = desc.String, by.String, created.Time
		out = append(out, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing versions of script %q of vhost %q: %w", name, vhost, err)
	}
	return out, nil
}

func (s *sqlStorage) GetMLScript(ctx context.Context, vhost, name string) (storage.MLScript, error) {
	sc := storage.MLScript{VHost: vhost, Name: name}
	var src, desc, by sql.NullString
	var created sql.NullTime
	err := s.queryRow(ctx, s.queries.get(QueryGetLatestMLScript), vhost, name, vhost, name).
		Scan(&sc.Version, &sc.SHA256, &src, &desc, &by, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.MLScript{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.MLScript{}, fmt.Errorf("reading script %q of vhost %q: %w", name, vhost, err)
	}
	sc.Source, sc.Description, sc.CreatedBy, sc.CreatedAt = src.String, desc.String, by.String, created.Time
	return sc, nil
}

// PutMLScript inserts the next version. Two saves racing for the same version
// number collide on the primary key; the loser reads the new latest and tries
// once more.
func (s *sqlStorage) PutMLScript(ctx context.Context, sc storage.MLScript) (storage.MLScript, error) {
	if err := storage.ValidateMLScript(sc); err != nil {
		return storage.MLScript{}, err
	}
	sc.SHA256 = storage.MLScriptSHA256(sc.Source)
	var lastErr error
	for range 2 {
		latest, err := s.GetMLScript(ctx, sc.VHost, sc.Name)
		switch {
		case errors.Is(err, storage.ErrNotFound):
			sc.Version = 1
		case err != nil:
			return storage.MLScript{}, err
		case latest.SHA256 == sc.SHA256:
			return latest, nil
		default:
			sc.Version = latest.Version + 1
		}
		sc.CreatedAt = time.Now().UTC()
		_, lastErr = s.exec(ctx, s.queries.get(QueryInsertMLScript),
			mlScriptID(sc.VHost, sc.Name, sc.Version), sc.VHost, sc.Name, sc.Version, sc.SHA256,
			sc.Source, sc.Description, sc.CreatedBy, sc.CreatedAt)
		if lastErr == nil {
			return sc, nil
		}
	}
	return storage.MLScript{}, fmt.Errorf("saving script %q of vhost %q: %w", sc.Name, sc.VHost, lastErr)
}

func (s *sqlStorage) DeleteMLScript(ctx context.Context, vhost, name string) error {
	res, err := s.exec(ctx, s.queries.get(QueryDeleteMLScript), vhost, name)
	if err != nil {
		return fmt.Errorf("deleting script %q of vhost %q: %w", name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) DeleteMLScripts(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteMLScriptsOfVHost), vhost); err != nil {
		return fmt.Errorf("deleting the scripts of vhost %q: %w", vhost, err)
	}
	return nil
}
