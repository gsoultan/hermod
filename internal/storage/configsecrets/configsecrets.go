// Package configsecrets decides which connector configuration values are
// credentials, and encrypts them at rest.
//
// Source and sink configuration is a flat map[string]string holding everything
// needed to reach an external system — host, port, table name, and also the
// password, API key or service-account JSON that authenticates to it. Those
// credentials are usually for systems considerably more sensitive than Hermod,
// so they are encrypted before they reach the metadata database and decrypted
// on the way back out.
//
// The rule below deliberately fails *closed*. The previous one was a flat list
// of eight exact key names, duplicated between the SQL and MongoDB backends,
// and a key nobody thought to add was written in plaintext with no warning.
// That is how ftp_password, api_key, client_secret, credentials_json (a GCP
// private key) and two DSNs ended up unencrypted next to password, which was
// handled correctly. Matching by shape means a connector added next year with
// an smtp_password is covered on the day it is written.
package configsecrets

import (
	"fmt"
	"log"
	"strings"

	"github.com/user/hermod/pkg/security/crypto"
)

// prefix marks a stored value as ciphertext. Decrypt keys off this rather than
// off the key name, which is what makes the rule below safe to widen: values
// written under an older, narrower rule are still plaintext, carry no prefix,
// and keep working until they are next saved.
const prefix = "enc:"

// sensitiveExact are credential names that the suffix rule does not catch.
var sensitiveExact = map[string]bool{
	"password":          true,
	"connection_string": true,
	"uri":               true,
	"token":             true,
	"secret":            true,
	"key":               true,
	"dsn":               true,
	"credentials_json":  true, // GCP service account, contains a private key
	"broker_url":        true, // amqp://user:pass@host
	"webhook_url":       true, // the path is the secret for Slack and friends
}

// sensitiveSuffix matches by shape, so new connectors are covered by default.
var sensitiveSuffix = []string{
	"_password", "_passwd", "_secret", "_token", "_key",
	"_dsn", "_credentials", "_connection_string", "_uri",
}

// notSensitive are names that match a rule above but are not credentials.
// Encrypting these would hide an operational value from anyone reading the
// database, and buys nothing. Each entry needs a reason.
var notSensitive = map[string]bool{
	"s3_key":                   true, // object key *prefix* — a path (factory sets S3KeyPrefix from it)
	"key_prefix":               true, // object key prefix
	"keyspace":                 true, // Cassandra keyspace name
	"idempotency_key_template": true, // template string, not a key
	"partition_key":            true, // routing, not authentication
	"routing_key":              true, // routing, not authentication
	"sort_key":                 true, // schema, not authentication
	"primary_key":              true, // schema, not authentication
}

// IsSensitive reports whether a configuration key holds a credential.
func IsSensitive(key string) bool {
	k := strings.ToLower(key)
	if notSensitive[k] {
		return false
	}
	if sensitiveExact[k] {
		return true
	}
	for _, suf := range sensitiveSuffix {
		if strings.HasSuffix(k, suf) {
			return true
		}
	}
	// Catches passphrase, db_password_file and anything else spelled with it.
	return strings.Contains(k, "password") || strings.Contains(k, "passwd")
}

// Encrypt returns a copy of config with every credential encrypted.
//
// A value that fails to encrypt is dropped rather than stored in the clear.
// Losing a credential is recoverable — the operator re-enters it — whereas
// writing it in plaintext is not, because it stays readable in every backup
// taken afterwards.
func Encrypt(config map[string]string) map[string]string {
	out := make(map[string]string, len(config))
	for k, v := range config {
		if v == "" || !IsSensitive(k) {
			out[k] = v
			continue
		}
		enc, err := crypto.Encrypt(v)
		if err != nil {
			log.Printf("configsecrets: cannot encrypt %q, dropping it rather than storing it in plaintext: %v", k, err)
			continue
		}
		out[k] = prefix + enc
	}
	return out
}

// Decrypt returns a copy of config with every encrypted value opened.
//
// A value that cannot be decrypted is blanked, not passed through. Returning
// the raw "enc:..." string — as this did — meant a connector received
// ciphertext as its password and failed to authenticate against the real
// database, with nothing in the logs connecting that to the master key. The
// usual cause is a key rotated without re-encrypting, so the message says so.
func Decrypt(config map[string]string) map[string]string {
	out := make(map[string]string, len(config))
	for k, v := range config {
		if !strings.HasPrefix(v, prefix) {
			out[k] = v
			continue
		}
		dec, err := crypto.Decrypt(strings.TrimPrefix(v, prefix))
		if err != nil {
			log.Printf("configsecrets: cannot decrypt %q with the configured master key "+
				"(was it rotated without re-encrypting?); the value is unavailable: %v", k, err)
			out[k] = ""
			continue
		}
		out[k] = dec
	}
	return out
}

// IsEncrypted reports whether a stored value is ciphertext. Re-encryption uses
// it to tell values that need rewriting from ones already in the clear.
func IsEncrypted(value string) bool { return strings.HasPrefix(value, prefix) }

// ReEncrypt takes a config map exactly as stored — ciphertext still wrapped —
// and returns it re-encrypted under newKey.
//
// It refuses rather than guesses. A value that will not open under the current
// master key is reported as an error and nothing is rewritten, because the
// alternative is writing back the blank that Decrypt substitutes and destroying
// a credential that a corrected key could still have recovered. That case is
// real: it is what a previous rotation without re-encryption leaves behind.
func ReEncrypt(stored map[string]string, newKey string) (map[string]string, error) {
	out := make(map[string]string, len(stored))
	for k, v := range stored {
		if !IsEncrypted(v) {
			// Plaintext, either a non-secret or a value written under an older
			// and narrower rule. Encrypt it now if it is a credential.
			if v == "" || !IsSensitive(k) {
				out[k] = v
				continue
			}
			enc, err := crypto.EncryptWith(newKey, v)
			if err != nil {
				return nil, fmt.Errorf("encrypting %q under the new key: %w", k, err)
			}
			out[k] = prefix + enc
			continue
		}

		plain, err := crypto.Decrypt(strings.TrimPrefix(v, prefix))
		if err != nil {
			return nil, fmt.Errorf("%q cannot be read with the current master key, so it "+
				"cannot be re-encrypted; rotating now would replace it with a blank: %w", k, err)
		}
		enc, err := crypto.EncryptWith(newKey, plain)
		if err != nil {
			return nil, fmt.Errorf("re-encrypting %q under the new key: %w", k, err)
		}
		out[k] = prefix + enc
	}
	return out, nil
}
