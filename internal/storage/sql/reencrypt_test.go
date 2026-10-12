package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/storage/configsecrets"
	"github.com/gsoultan/hermod/pkg/security/crypto"
	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Key rotation, end to end against real storage.
//
// PUT /api/config/crypto swapped the master key and returned 204. Everything
// already stored stayed encrypted under the old key, which the process no
// longer had — so every source and sink credential in the deployment became
// unreadable in one request, with a success response.
// ---------------------------------------------------------------------------

func newRotationStorage(t *testing.T) *sqlStorage {
	t.Helper()
	dsn := fmt.Sprintf("file:rot_%s?mode=memory&cache=shared&_pragma=foreign_keys(ON)", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "sqlite").(*sqlStorage)
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

// withKey installs a master key for one test and restores the previous one.
func withKey(t *testing.T, key string) {
	t.Helper()
	t.Cleanup(func() { crypto.SetMasterKey(crypto.DefaultMasterKey) })
	crypto.SetMasterKey(key)
}

// TestRotationKeepsCredentialsReadable is the whole point.
func TestRotationKeepsCredentialsReadable(t *testing.T) {
	const oldKey = "old-master-key-at-least-16"
	const newKey = "new-master-key-at-least-16"
	const password = "hunter2"

	ctx := t.Context()
	s := newRotationStorage(t)
	withKey(t, oldKey)

	if err := s.CreateSource(ctx, storage.Source{
		ID: "src-1", Name: "prod-db", Type: "postgres",
		Config: map[string]string{"host": "db.internal", "password": password},
	}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if err := s.CreateSink(ctx, storage.Sink{
		ID: "snk-1", Name: "warehouse", Type: "snowflake",
		Config: map[string]string{"api_key": "ak_live_1", "table": "events"},
	}); err != nil {
		t.Fatalf("CreateSink: %v", err)
	}

	// Rotate the way the handler now does: re-encrypt first, then install.
	if err := s.ReEncryptSecrets(ctx, newKey); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}
	crypto.SetMasterKey(newKey)

	got, err := s.GetSource(ctx, "src-1")
	if err != nil {
		t.Fatalf("GetSource after rotation: %v", err)
	}
	if got.Config["password"] != password {
		t.Errorf("password reads back as %q after rotation, want %q; "+
			"every connector in the deployment would fail to authenticate",
			got.Config["password"], password)
	}
	if got.Config["host"] != "db.internal" {
		t.Errorf("non-secret value changed during rotation: %q", got.Config["host"])
	}

	sink, err := s.GetSink(ctx, "snk-1")
	if err != nil {
		t.Fatalf("GetSink after rotation: %v", err)
	}
	if sink.Config["api_key"] != "ak_live_1" {
		t.Errorf("sink api_key reads back as %q after rotation, want %q",
			sink.Config["api_key"], "ak_live_1")
	}
}

// TestRotationWithoutReEncryptionIsDetectable documents the old behaviour, so
// that if the re-encryption step is ever dropped the consequence is written
// down in a test rather than discovered in production.
//
// It also pins the second half of the fix: an unreadable value comes back
// blank, never as raw ciphertext. Handing "enc:..." to a database driver as a
// password is what made the original bug so hard to trace.
func TestRotationWithoutReEncryptionIsDetectable(t *testing.T) {
	ctx := t.Context()
	s := newRotationStorage(t)
	withKey(t, "old-master-key-at-least-16")

	if err := s.CreateSource(ctx, storage.Source{
		ID: "src-2", Name: "db", Type: "postgres",
		Config: map[string]string{"password": "hunter2"},
	}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	// The old, broken rotation: swap the key and touch nothing else.
	crypto.SetMasterKey("a-totally-different-key-16")

	got, err := s.GetSource(ctx, "src-2")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if strings.HasPrefix(got.Config["password"], "enc:") {
		t.Errorf("undecryptable password came back as ciphertext %q; a connector "+
			"would use it verbatim and the cause would be invisible", got.Config["password"])
	}
	if got.Config["password"] == "hunter2" {
		t.Error("the key did not actually change")
	}
}

// TestReEncryptRefusesWhenAValueIsUnreadable protects against a second rotation
// making a bad situation permanent.
//
// If a previous rotation already stranded values under a lost key, re-encrypting
// would read them as blanks and write those blanks back — destroying credentials
// that restoring the old key could still have recovered. It has to refuse.
func TestReEncryptRefusesWhenAValueIsUnreadable(t *testing.T) {
	ctx := t.Context()
	s := newRotationStorage(t)
	withKey(t, "first-master-key-at-least-16")

	if err := s.CreateSource(ctx, storage.Source{
		ID: "src-3", Name: "db", Type: "postgres",
		Config: map[string]string{"password": "hunter2"},
	}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}

	// Someone already rotated without re-encrypting; the value is stranded.
	crypto.SetMasterKey("second-master-key-at-least-16")

	err := s.ReEncryptSecrets(ctx, "third-master-key-at-least-16")
	if err == nil {
		t.Fatal("re-encryption accepted a value it could not read; it would have " +
			"written back a blank and destroyed a recoverable credential")
	}

	// And it must have written nothing: restoring the original key still works.
	crypto.SetMasterKey("first-master-key-at-least-16")
	got, gerr := s.GetSource(ctx, "src-3")
	if gerr != nil {
		t.Fatalf("GetSource: %v", gerr)
	}
	if got.Config["password"] != "hunter2" {
		t.Errorf("password is %q after a refused rotation, want %q; the refusal was "+
			"not atomic and data was modified anyway", got.Config["password"], "hunter2")
	}
}

// TestReEncryptUpgradesPlaintextWrittenByAnOlderRule closes the loop with the
// widened sensitive-key rule: values that an older build stored in the clear —
// ftp_password, api_key, client_secret — are encrypted the first time secrets
// are rewritten, rather than staying plaintext until someone edits them.
func TestReEncryptUpgradesPlaintextWrittenByAnOlderRule(t *testing.T) {
	ctx := t.Context()
	s := newRotationStorage(t)
	withKey(t, "master-key-at-least-16-chars")

	// Write config straight to the column, as an older build with the narrow
	// allowlist would have: the credential in the clear.
	if err := s.CreateSource(ctx, storage.Source{ID: "src-4", Name: "ftp", Type: "ftp"}); err != nil {
		t.Fatalf("CreateSource: %v", err)
	}
	if _, err := s.exec(ctx,
		`UPDATE sources SET config = ? WHERE id = ?`,
		`{"ftp_password":"plaintext-secret","host":"ftp.example.com"}`, "src-4"); err != nil {
		t.Fatalf("seeding legacy plaintext: %v", err)
	}

	if err := s.ReEncryptSecrets(ctx, "master-key-at-least-16-chars"); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}

	// The stored column must no longer contain the secret.
	var raw string
	if err := s.queryRow(ctx, `SELECT config FROM sources WHERE id = ?`, "src-4").Scan(&raw); err != nil {
		t.Fatalf("reading raw config: %v", err)
	}
	if strings.Contains(raw, "plaintext-secret") {
		t.Errorf("ftp_password is still stored in the clear after re-encryption: %s", raw)
	}
	if !strings.Contains(raw, "ftp.example.com") {
		t.Errorf("non-secret host was lost during re-encryption: %s", raw)
	}

	// And it still reads back correctly.
	got, err := s.GetSource(ctx, "src-4")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if got.Config["ftp_password"] != "plaintext-secret" {
		t.Errorf("ftp_password reads back as %q, want %q", got.Config["ftp_password"], "plaintext-secret")
	}
}

// The ML worker connected from the console keeps its token in settings,
// encrypted like a connector's credentials. A rotation that left it behind
// would disconnect training with no word of why.
func TestRotationKeepsTheMLWorkerTokenReadable(t *testing.T) {
	rotateMLWorkerSetting(t, newRotationStorage(t))
}

// On Postgres the settings upsert is the dialect's own ($n placeholders, ON
// CONFLICT), run inside the rotation's transaction. A rotation rewrites every
// connector in the database, so this one runs in a schema of its own rather
// than over the rows other tests share.
func TestRotationKeepsTheMLWorkerTokenReadableOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be opened: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("rot_ml_worker_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewSQLStorage(db, "pgx").(*sqlStorage)
	if err := s.Init(t.Context()); err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be initialised: %v", err)
	}
	rotateMLWorkerSetting(t, s)
}

func rotateMLWorkerSetting(t *testing.T, s *sqlStorage) {
	t.Helper()
	const oldKey = "old-master-key-at-least-16"
	const newKey = "new-master-key-at-least-16"
	ctx := t.Context()
	withKey(t, oldKey)

	stored, _ := json.Marshal(configsecrets.Encrypt(map[string]string{"url": "http://hermod-ml:8090", "token": "s3cret"}))
	if err := s.SaveSetting(ctx, storage.MLWorkerSettingKey, string(stored)); err != nil {
		t.Fatalf("SaveSetting: %v", err)
	}
	raw, err := s.GetSetting(ctx, storage.MLWorkerSettingKey)
	if err != nil || raw != string(stored) {
		t.Fatalf("GetSetting = %q, %v; want what was saved", raw, err)
	}

	if err := s.ReEncryptSecrets(ctx, newKey); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}
	crypto.SetMasterKey(newKey)

	raw, err = s.GetSetting(ctx, storage.MLWorkerSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("setting after rotation %q: %v", raw, err)
	}
	if strings.Contains(raw, "s3cret") {
		t.Errorf("rotation wrote the token in the clear: %s", raw)
	}
	if plain := configsecrets.Decrypt(got); plain["token"] != "s3cret" || plain["url"] != "http://hermod-ml:8090" {
		t.Errorf("after rotation the connection reads %v", plain)
	}
}

// No ML worker saved: rotation has nothing to rewrite and must not create one.
func TestRotationWithoutAnMLWorkerLeavesNoSetting(t *testing.T) {
	ctx := t.Context()
	s := newRotationStorage(t)
	withKey(t, "old-master-key-at-least-16")
	if err := s.ReEncryptSecrets(ctx, "new-master-key-at-least-16"); err != nil {
		t.Fatalf("ReEncryptSecrets: %v", err)
	}
	if raw, _ := s.GetSetting(ctx, storage.MLWorkerSettingKey); raw != "" {
		t.Errorf("rotation created %q", raw)
	}
}
