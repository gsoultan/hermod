package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsoultan/hermod/internal/api/handlers"
	"github.com/gsoultan/hermod/internal/storage"
	"github.com/gsoultan/hermod/internal/testutil"
)

// ---------------------------------------------------------------------------
// Backup and restore.
//
// These two endpoints are what an operator reaches for on their worst day: the
// export is taken before a risky change, and the import runs after something
// has already gone wrong. Both silently ignored every error they could hit.
// ---------------------------------------------------------------------------

type backupStorage struct {
	testutil.BaseMockStorage

	sources   []storage.Source
	sinks     []storage.Sink
	workflows []storage.Workflow

	listSourcesErr error
	createErr      error

	created int
	updated int
}

func (m *backupStorage) ListSources(context.Context, storage.CommonFilter) ([]storage.Source, int, error) {
	if m.listSourcesErr != nil {
		return nil, 0, m.listSourcesErr
	}
	return m.sources, len(m.sources), nil
}

func (m *backupStorage) ListSinks(context.Context, storage.CommonFilter) ([]storage.Sink, int, error) {
	return m.sinks, len(m.sinks), nil
}

func (m *backupStorage) ListWorkflows(context.Context, storage.CommonFilter) ([]storage.Workflow, int, error) {
	return m.workflows, len(m.workflows), nil
}

func (m *backupStorage) ListVHosts(context.Context, storage.CommonFilter) ([]storage.VHost, int, error) {
	return nil, 0, nil
}
func (m *backupStorage) ListWorkspaces(context.Context) ([]storage.Workspace, error) {
	return nil, nil
}
func (m *backupStorage) GetSetting(context.Context, string) (string, error) {
	return "", storage.ErrNotFound
}
func (m *backupStorage) SaveSetting(context.Context, string, string) error { return nil }

func (m *backupStorage) GetSource(context.Context, string) (storage.Source, error) {
	return storage.Source{}, storage.ErrNotFound
}
func (m *backupStorage) GetSink(context.Context, string) (storage.Sink, error) {
	return storage.Sink{}, storage.ErrNotFound
}
func (m *backupStorage) GetWorkflow(context.Context, string) (storage.Workflow, error) {
	return storage.Workflow{}, storage.ErrNotFound
}
func (m *backupStorage) GetVHost(context.Context, string) (storage.VHost, error) {
	return storage.VHost{}, storage.ErrNotFound
}

func (m *backupStorage) CreateSource(context.Context, storage.Source) error {
	m.created++
	return m.createErr
}
func (m *backupStorage) CreateSink(context.Context, storage.Sink) error {
	m.created++
	return m.createErr
}
func (m *backupStorage) CreateWorkflow(context.Context, storage.Workflow) error {
	m.created++
	return m.createErr
}
func (m *backupStorage) CreateVHost(context.Context, storage.VHost) error { return m.createErr }
func (m *backupStorage) UpdateSource(context.Context, storage.Source) error {
	m.updated++
	return nil
}
func (m *backupStorage) UpdateSink(context.Context, storage.Sink) error {
	m.updated++
	return nil
}
func (m *backupStorage) UpdateWorkflow(context.Context, storage.Workflow) error {
	m.updated++
	return nil
}

func adminRequest(method, target string, body []byte) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequestWithContext(context.Background(), method, target, nil)
	} else {
		r = httptest.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(body))
	}
	u := &storage.User{ID: "u-1", Username: "admin", Role: storage.RoleAdministrator}
	return r.WithContext(context.WithValue(r.Context(), handlers.UserContextKey, u))
}

func newBackupHandler(store storage.Storage) *InfraHandler {
	return NewInfraHandler(&handlers.Handler{Storage: store})
}

// TestImportReportsStorageFailures is the restore-day property.
//
// ImportConfig ran every create and update as `_ = h.Storage.CreateSource(...)`
// and finished with an unconditional 204. A restore into a database that
// rejected every row — wrong schema, a constraint violation, an outage — told
// the operator it had succeeded. They would then believe their configuration
// was back.
func TestImportReportsStorageFailures(t *testing.T) {
	backup := BackupData{
		Sources:   []storage.Source{{ID: "src-1", Name: "db"}},
		Sinks:     []storage.Sink{{ID: "snk-1", Name: "warehouse"}},
		Workflows: []storage.Workflow{{ID: "wf-1", Name: "pipeline"}},
	}
	body, err := json.Marshal(backup)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	store := &backupStorage{createErr: errors.New("insert violates a constraint")}
	rec := httptest.NewRecorder()

	newBackupHandler(store).ImportConfig(rec, adminRequest(http.MethodPost, "/api/backup/import", body))

	if rec.Code < 400 {
		t.Errorf("import returned %d after every write failed; the operator is told "+
			"the restore worked and their configuration is not there", rec.Code)
	}
	if store.created == 0 {
		t.Fatal("the import did not attempt any writes; the test proves nothing")
	}
}

// TestImportSucceedsWhenStorageAccepts keeps the failure path from being so
// strict that a good restore reports an error.
func TestImportSucceedsWhenStorageAccepts(t *testing.T) {
	backup := BackupData{
		Sources:   []storage.Source{{ID: "src-1", Name: "db"}},
		Sinks:     []storage.Sink{{ID: "snk-1", Name: "warehouse"}},
		Workflows: []storage.Workflow{{ID: "wf-1", Name: "pipeline"}},
	}
	body, _ := json.Marshal(backup)

	store := &backupStorage{}
	rec := httptest.NewRecorder()

	newBackupHandler(store).ImportConfig(rec, adminRequest(http.MethodPost, "/api/backup/import", body))

	if rec.Code >= 400 {
		t.Errorf("a clean restore returned %d: %s", rec.Code, rec.Body.String())
	}
	if store.created != 3 {
		t.Errorf("restored %d objects, want 3", store.created)
	}
}

// TestExportFailsLoudlyRatherThanDownloadingAnEmptyBackup covers the more
// dangerous of the two.
//
// ExportConfig discarded the error from every list call
// (`data.Sources, _, _ = ...`) and always wrote 200 with a
// Content-Disposition attachment header. A database that was down produced a
// file named hermod-config-backup.json containing empty arrays. Nothing about
// it looks wrong until someone tries to restore from it.
func TestExportFailsLoudlyRatherThanDownloadingAnEmptyBackup(t *testing.T) {
	store := &backupStorage{listSourcesErr: errors.New("connection refused")}
	rec := httptest.NewRecorder()

	newBackupHandler(store).ExportConfig(rec, adminRequest(http.MethodGet, "/api/backup/export", nil))

	if rec.Code < 400 {
		t.Errorf("export returned %d while the database was unreachable; the operator "+
			"saves an empty file and believes it is a backup. Body: %s", rec.Code, rec.Body.String())
	}
}

// TestExportRoundTripsThroughImport is the property the pair exists for.
func TestExportRoundTripsThroughImport(t *testing.T) {
	original := &backupStorage{
		sources:   []storage.Source{{ID: "src-1", Name: "db", Config: map[string]string{"host": "h", "password": "p"}}},
		sinks:     []storage.Sink{{ID: "snk-1", Name: "warehouse"}},
		workflows: []storage.Workflow{{ID: "wf-1", Name: "pipeline"}},
	}

	rec := httptest.NewRecorder()
	newBackupHandler(original).ExportConfig(rec, adminRequest(http.MethodGet, "/api/backup/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", rec.Code, rec.Body.String())
	}

	var exported BackupData
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("the exported file is not valid JSON: %v", err)
	}
	if len(exported.Sources) != 1 || exported.Sources[0].ID != "src-1" {
		t.Fatalf("export lost the sources: %+v", exported.Sources)
	}
	if got := exported.Sources[0].Config["password"]; got != "p" {
		t.Errorf("export wrote password %q; a backup that cannot restore the credential "+
			"is not a backup, so this must be the plaintext", got)
	}

	// Restore into a fresh instance.
	restored := &backupStorage{}
	rec2 := httptest.NewRecorder()
	newBackupHandler(restored).ImportConfig(rec2,
		adminRequest(http.MethodPost, "/api/backup/import", rec.Body.Bytes()))

	if rec2.Code >= 400 {
		t.Fatalf("restoring an exported backup failed with %d: %s", rec2.Code, rec2.Body.String())
	}
	if restored.created != 3 {
		t.Errorf("restore created %d objects from a backup holding 3", restored.created)
	}
}

// TestExportRefusesToTruncateSilently covers a cap nobody would notice.
//
// The export lists with Limit: 1000. A deployment with more objects than that
// gets a backup that is missing the overflow, with no warning anywhere — and it
// is only discovered during a restore, which is the worst possible moment.
func TestExportRefusesToTruncateSilently(t *testing.T) {
	store := &backupStorage{}
	for i := range exportLimit + 1 {
		store.sources = append(store.sources, storage.Source{ID: string(rune('a' + i%26))})
	}
	// The mock reports the true total, as a real backend does.
	rec := httptest.NewRecorder()

	newBackupHandler(store).ExportConfig(rec, adminRequest(http.MethodGet, "/api/backup/export", nil))

	if rec.Code < 400 {
		t.Errorf("export returned %d for a deployment with more than %d sources; the "+
			"backup silently omits the overflow", rec.Code, exportLimit)
	}
}
