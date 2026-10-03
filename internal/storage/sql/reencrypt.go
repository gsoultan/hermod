package sql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gsoultan/hermod/internal/storage/configsecrets"
	"github.com/gsoultan/hermod/pkg/security/crypto"
)

// secretTables are the tables holding connector configuration. Both are
// literals, never caller input, which is what makes the interpolation below
// safe.
var secretTables = []string{"sources", "sinks"}

// vhostSecretsTable holds one encrypted value per row rather than a config
// document; see vhost_secrets.go.
const vhostSecretsTable = "vhost_secrets"

// configRow is one row's raw, still-encrypted content: a connector's config
// document, or a vhost secret's value.
type configRow struct {
	table  string
	column string
	id     string
	config string
}

// ReEncryptSecrets rewrites every stored credential under newKey.
//
// This is the half of key rotation that was missing. Rotation used to swap the
// master key and return 204: every password, API key and service-account
// document already in the database had been encrypted under the old key, so
// none of them could be read afterwards. Pipelines did not fail cleanly either
// — the storage layer handed the raw ciphertext to connectors as though it were
// the plaintext, so operators saw authentication failures against their own
// databases with nothing pointing at the key change.
//
// The new ciphertext is produced with crypto.EncryptWith before anything is
// written, and the writes go in one transaction. The process key is *not*
// touched here: the caller installs it only once this has committed, so a
// failure at any point leaves every row readable under the key still in force.
func (s *sqlStorage) ReEncryptSecrets(ctx context.Context, newKey string) error {
	if newKey == "" {
		return errors.New("re-encrypt: empty key")
	}

	rows, err := s.readConfigRows(ctx)
	if err != nil {
		return err
	}
	updates, err := reEncryptRows(rows, newKey)
	if err != nil {
		return err
	}

	// A vhost's secrets are encrypted under the same key. Left out, they would
	// become unreadable the moment the caller installs the new one.
	secretRows, err := s.readTableColumn(ctx, vhostSecretsTable, "value")
	if err != nil {
		return err
	}
	secretUpdates, err := reEncryptValues(secretRows, newKey)
	if err != nil {
		return err
	}

	return s.writeConfigRows(ctx, append(updates, secretUpdates...))
}

// readConfigRows reads the raw config column, bypassing the decryption that the
// normal accessors apply — re-encryption needs to see the stored form.
func (s *sqlStorage) readConfigRows(ctx context.Context) ([]configRow, error) {
	var out []configRow
	for _, table := range secretTables {
		rows, err := s.readTableColumn(ctx, table, "config")
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (s *sqlStorage) readTableColumn(ctx context.Context, table, column string) ([]configRow, error) {
	//nolint:gosec // table and column are literals in this file, not caller input.
	rs, err := s.query(ctx, "SELECT id, "+column+" FROM "+table)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", table, err)
	}
	defer func() { _ = rs.Close() }()

	var out []configRow
	for rs.Next() {
		r := configRow{table: table, column: column}
		if err := rs.Scan(&r.id, &r.config); err != nil {
			return nil, fmt.Errorf("scanning %s: %w", table, err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", table, err)
	}
	return out, nil
}

// reEncryptRows produces the new ciphertext for every row.
//
// If any single value cannot be read under the current key, this returns an
// error and the caller writes nothing at all. A partial rotation is worse than
// none: half the credentials would become unrecoverable, and which half would
// not be recorded anywhere.
func reEncryptRows(rows []configRow, newKey string) ([]configRow, error) {
	updates := make([]configRow, 0, len(rows))
	for _, r := range rows {
		cfg := map[string]string{}
		if r.config != "" {
			if err := json.Unmarshal([]byte(r.config), &cfg); err != nil {
				return nil, fmt.Errorf("%s %s has unreadable config: %w", r.table, r.id, err)
			}
		}
		next, err := configsecrets.ReEncrypt(cfg, newKey)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", r.table, r.id, err)
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", r.table, r.id, err)
		}
		updates = append(updates, configRow{table: r.table, column: r.column, id: r.id, config: string(encoded)})
	}
	return updates, nil
}

// reEncryptValues produces the new ciphertext for rows whose column is one
// encrypted value. As with reEncryptRows, one unreadable value fails the whole
// rotation and nothing is written.
func reEncryptValues(rows []configRow, newKey string) ([]configRow, error) {
	updates := make([]configRow, 0, len(rows))
	for _, r := range rows {
		plain, err := crypto.Decrypt(r.config)
		if err != nil {
			return nil, fmt.Errorf("%s %s cannot be read under the current key: %w", r.table, r.id, err)
		}
		next, err := crypto.EncryptWith(newKey, plain)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", r.table, r.id, err)
		}
		updates = append(updates, configRow{table: r.table, column: r.column, id: r.id, config: next})
	}
	return updates, nil
}

func (s *sqlStorage) writeConfigRows(ctx context.Context, updates []configRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("re-encrypt transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, u := range updates {
		// Rebind placeholders for the driver, exactly as s.exec does outside a
		// transaction; a raw '?' reaches Postgres as a syntax error.
		//nolint:gosec // u.table and u.column are literals in this file, not caller input.
		stmt := s.prepareQuery("UPDATE " + u.table + " SET " + u.column + " = ? WHERE id = ?")
		if _, err := tx.ExecContext(ctx, stmt, u.config, u.id); err != nil {
			return fmt.Errorf("rewriting %s %s: %w", u.table, u.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing re-encrypted secrets: %w", err)
	}
	return nil
}
