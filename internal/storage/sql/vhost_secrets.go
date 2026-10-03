package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// vhostSecretID is the row key: vhost + "/" + name. A secret name cannot hold
// a slash (storage.ValidVHostSecretName), so the last one always separates the
// two even when the vhost's own name contains one.
func vhostSecretID(vhost, name string) string {
	return vhost + "/" + name
}

func (s *sqlStorage) ListVHostSecrets(ctx context.Context, vhost string) ([]storage.VHostSecret, error) {
	rows, err := s.query(ctx, s.queries.get(QueryListVHostSecrets), vhost)
	if err != nil {
		return nil, fmt.Errorf("listing secrets of vhost %q: %w", vhost, err)
	}
	defer func() { _ = rows.Close() }()

	var out []storage.VHostSecret
	for rows.Next() {
		secret := storage.VHostSecret{VHost: vhost}
		var by sql.NullString
		var created, updated sql.NullTime
		if err := rows.Scan(&secret.Name, &by, &created, &updated); err != nil {
			return nil, fmt.Errorf("listing secrets of vhost %q: %w", vhost, err)
		}
		secret.UpdatedBy, secret.CreatedAt, secret.UpdatedAt = by.String, created.Time, updated.Time
		out = append(out, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing secrets of vhost %q: %w", vhost, err)
	}
	return out, nil
}

// GetVHostSecret returns the secret with its value decrypted. A value that
// cannot be decrypted is an error, never the ciphertext: handing that to a
// workflow as though it were the secret would send it to whatever the workflow
// calls.
func (s *sqlStorage) GetVHostSecret(ctx context.Context, vhost, name string) (storage.VHostSecret, error) {
	secret := storage.VHostSecret{VHost: vhost, Name: name}
	var stored string
	var by sql.NullString
	var created, updated sql.NullTime
	err := s.queryRow(ctx, s.queries.get(QueryGetVHostSecret), vhostSecretID(vhost, name)).
		Scan(&stored, &by, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.VHostSecret{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.VHostSecret{}, fmt.Errorf("reading secret %q of vhost %q: %w", name, vhost, err)
	}
	value, err := crypto.Decrypt(stored)
	if err != nil {
		return storage.VHostSecret{}, fmt.Errorf("secret %q of vhost %q cannot be decrypted with the current master key: %w", name, vhost, err)
	}
	secret.Value, secret.UpdatedBy, secret.CreatedAt, secret.UpdatedAt = value, by.String, created.Time, updated.Time
	return secret, nil
}

// PutVHostSecret updates the row if the vhost already holds the name and
// inserts it otherwise. It is an update-then-insert rather than an upsert
// because the three dialects here spell an upsert three ways; if another
// writer inserts in between, the insert fails on the key and the update is
// tried once more.
func (s *sqlStorage) PutVHostSecret(ctx context.Context, secret storage.VHostSecret) error {
	if err := storage.ValidateVHostSecret(secret); err != nil {
		return err
	}
	stored, err := crypto.Encrypt(secret.Value)
	if err != nil {
		return fmt.Errorf("encrypting secret %q of vhost %q: %w", secret.Name, secret.VHost, err)
	}
	id := vhostSecretID(secret.VHost, secret.Name)
	now := time.Now().UTC()

	updated, err := s.updateVHostSecret(ctx, id, stored, secret.UpdatedBy, now)
	if err != nil || updated {
		return err
	}
	if _, err := s.exec(ctx, s.queries.get(QueryInsertVHostSecret),
		id, secret.VHost, secret.Name, stored, secret.UpdatedBy, now, now); err != nil {
		if updated, retryErr := s.updateVHostSecret(ctx, id, stored, secret.UpdatedBy, now); retryErr == nil && updated {
			return nil
		}
		return fmt.Errorf("saving secret %q of vhost %q: %w", secret.Name, secret.VHost, err)
	}
	return nil
}

func (s *sqlStorage) updateVHostSecret(ctx context.Context, id, stored, by string, at time.Time) (bool, error) {
	res, err := s.exec(ctx, s.queries.get(QueryUpdateVHostSecret), stored, by, at, id)
	if err != nil {
		return false, fmt.Errorf("saving secret: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("saving secret: %w", err)
	}
	return n > 0, nil
}

func (s *sqlStorage) DeleteVHostSecret(ctx context.Context, vhost, name string) error {
	res, err := s.exec(ctx, s.queries.get(QueryDeleteVHostSecret), vhostSecretID(vhost, name))
	if err != nil {
		return fmt.Errorf("deleting secret %q of vhost %q: %w", name, vhost, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

func (s *sqlStorage) DeleteVHostSecrets(ctx context.Context, vhost string) error {
	if _, err := s.exec(ctx, s.queries.get(QueryDeleteVHostSecrets), vhost); err != nil {
		return fmt.Errorf("deleting the secrets of vhost %q: %w", vhost, err)
	}
	return nil
}
