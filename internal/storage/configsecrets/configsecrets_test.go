package configsecrets

import (
	"strings"
	"testing"
)

// TestSecretBearingConfigKeysAreEncrypted covers what "encrypted at rest"
// actually means for connector credentials.
//
// Source and sink configuration is a flat map[string]string, and the storage
// layer encrypts a value only when its key appears in a hand-maintained list.
// That list fails *open*: a key nobody remembered to add is written to the
// database in plaintext, silently, alongside keys that are encrypted correctly.
//
// Every name below is read by internal/factory today. They are the credentials
// for the systems Hermod connects to — which are usually far more sensitive
// than Hermod itself.
func TestSecretBearingConfigKeysAreEncrypted(t *testing.T) {
	secrets := []struct {
		key  string
		what string
	}{
		{"password", "database password"},
		{"connection_string", "DSN with an embedded password"},
		{"token", "bearer token"},
		{"secret", "shared secret"},
		{"access_key", "cloud access key"},
		{"secret_key", "cloud secret key"},
		{"ftp_password", "FTP password"},
		{"api_key", "API key"},
		{"client_secret", "OAuth client secret"},
		{"access_token", "OAuth access token"},
		{"auth_token", "auth token"},
		{"security_token", "Salesforce security token"},
		{"device_token", "push device token"},
		{"credentials_json", "GCP service-account JSON, which contains a private key"},
		{"dsn", "DSN with an embedded password"},
		{"idempotency_dsn", "DSN with an embedded password"},
		{"s3_access_key", "S3 access key"},
		{"s3_secret_key", "S3 secret key"},
	}

	for _, tc := range secrets {
		t.Run(tc.key, func(t *testing.T) {
			const plaintext = "super-secret-value"
			got := Encrypt(map[string]string{tc.key: plaintext})[tc.key]

			if got == plaintext {
				t.Errorf("%s (%s) is stored verbatim; anyone who can read the metadata "+
					"database, a backup or a replica reads the credential", tc.key, tc.what)
			}
			if !strings.HasPrefix(got, "enc:") {
				t.Errorf("%s was not encrypted: stored as %q", tc.key, got)
			}
		})
	}
}

// TestNonSecretConfigKeysAreLeftAlone is the other half. Failing closed is only
// safe if it does not sweep in values that merely look like credentials.
//
// s3_key is an object key *prefix* — a path, not a secret (factory.go assigns
// it to S3KeyPrefix). Encrypting it would be wrong, and encrypting routing or
// partition keys would corrupt behaviour rather than protect anything.
func TestNonSecretConfigKeysAreLeftAlone(t *testing.T) {
	plain := []struct {
		key  string
		what string
	}{
		{"s3_key", "S3 object key prefix (a path)"},
		{"key_prefix", "object key prefix"},
		{"keyspace", "Cassandra keyspace name"},
		{"idempotency_key_template", "template string"},
		{"host", "hostname"},
		{"port", "port"},
		{"database", "database name"},
		{"table", "table name"},
		{"username", "username"},
	}

	for _, tc := range plain {
		t.Run(tc.key, func(t *testing.T) {
			const value = "not-a-secret"
			got := Encrypt(map[string]string{tc.key: value})[tc.key]
			if got != value {
				t.Errorf("%s (%s) was encrypted to %q; it is not a credential, and "+
					"encrypting it hides an operational value from anyone debugging the database",
					tc.key, tc.what, got)
			}
		})
	}
}

// TestEncryptDecryptRoundTrips is the property both halves rest on.
func TestEncryptDecryptRoundTrips(t *testing.T) {
	in := map[string]string{
		"password": "hunter2",
		"api_key":  "ak_live_123",
		"host":     "db.internal",
		"s3_key":   "exports/2026/",
	}

	out := Decrypt(Encrypt(in))

	for k, want := range in {
		if out[k] != want {
			t.Errorf("%s round-tripped to %q, want %q", k, out[k], want)
		}
	}
}

// TestDecryptDoesNotHandBackCiphertext pins the failure mode that turns a
// key-rotation mistake into a mystery.
//
// When a value cannot be decrypted — the master key was rotated without
// re-encrypting, say — decryptConfig returned the raw "enc:..." string as
// though it were the plaintext. That ciphertext then goes to a database driver
// as the password. The operator sees authentication failures against their own
// databases with nothing pointing at the key change.
func TestDecryptDoesNotHandBackCiphertext(t *testing.T) {
	const bogus = "enc:this-is-not-valid-ciphertext-under-any-key"

	got := Decrypt(map[string]string{"password": bogus})["password"]

	if strings.HasPrefix(got, "enc:") {
		t.Errorf("undecryptable value was passed through as %q; a connector would "+
			"receive ciphertext as its password and fail to authenticate with no "+
			"indication that the master key is the cause", got)
	}
}
