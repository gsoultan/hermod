package sql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/gsoultan/hermod/internal/storage"

	_ "modernc.org/sqlite"
)

// The remaining five lists, finishing what workflow_list_test.go and
// source_sink_list_test.go started.
//
// Users, vhosts and workers had both defects — a LIKE PostgreSQL folds no case
// for, and a LIMIT/OFFSET over no ORDER BY. Plugins had no order either. Logs
// and audit logs already order by timestamp, so only their search is fixed here;
// they are the two widest searches in the codebase (five columns each), and an
// audit trail that cannot be searched for "DELETE" because the row says "delete"
// is the one place this matters most.
//
// Every case here runs with PRAGMA case_sensitive_like = ON, which makes sqlite
// behave the way PostgreSQL always has. Without it these tests pass whether or
// not the bug is fixed.

// Each seed puts a token unique across the whole table in one column, so a
// search for it can only have matched through that column — and the token is
// searched in the case it is not stored in.
type adminCase struct {
	search, want, column string
}

func checkAdminSearch(t *testing.T, cases []adminCase, list func(search string) ([]string, int, error)) {
	t.Helper()
	for _, tc := range cases {
		got, total, err := list(tc.search)
		if err != nil {
			t.Errorf("searching for %q failed: %v", tc.search, err)
			continue
		}
		if len(got) != 1 || got[0] != tc.want || total != 1 {
			t.Errorf("searching for %q returned %v (total %d), want only %s\n"+
				"the match has to come through the %s column, and PostgreSQL's LIKE "+
				"compares it as stored", tc.search, got, total, tc.want, tc.column)
		}
	}
}

func TestUserSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	for _, u := range []storage.User{
		{ID: "usr-QQAlpha", Username: "one", FullName: "First One", Email: "one@x.test", Role: storage.RoleViewer},
		{ID: "usr-b", Username: "QQBravo", FullName: "Second", Email: "two@x.test", Role: storage.RoleViewer},
		{ID: "usr-c", Username: "three", FullName: "QQCharlie Third", Email: "three@x.test", Role: storage.RoleViewer},
		{ID: "usr-d", Username: "four", FullName: "Fourth", Email: "QQDelta@x.test", Role: storage.RoleViewer},
		{ID: "usr-e", Username: "five", FullName: "Fifth", Email: "five@x.test", Role: "QQEcho"},
	} {
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatalf("seed %s: %v", u.ID, err)
		}
	}

	checkAdminSearch(t, []adminCase{
		{"qqalpha", "usr-QQAlpha", "id"},
		{"QQBRAVO", "usr-b", "username"},
		{"qqcharlie", "usr-c", "full_name"},
		{"QQDELTA", "usr-d", "email"},
		{"qqecho", "usr-e", "role"},
	}, func(search string) ([]string, int, error) {
		got, total, err := s.ListUsers(ctx, storage.CommonFilter{Search: search})
		ids := make([]string, 0, len(got))
		for _, u := range got {
			ids = append(ids, u.ID)
		}
		return ids, total, err
	})
}

func TestVHostSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	for _, v := range []storage.VHost{
		{ID: "vh-QQAlpha", Name: "one", Description: "first"},
		{ID: "vh-b", Name: "QQBravo", Description: "second"},
		{ID: "vh-c", Name: "three", Description: "QQCharlie third"},
	} {
		if err := s.CreateVHost(ctx, v); err != nil {
			t.Fatalf("seed %s: %v", v.ID, err)
		}
	}

	checkAdminSearch(t, []adminCase{
		{"qqalpha", "vh-QQAlpha", "id"},
		{"QQBRAVO", "vh-b", "name"},
		{"qqcharlie", "vh-c", "description"},
	}, func(search string) ([]string, int, error) {
		got, total, err := s.ListVHosts(ctx, storage.CommonFilter{Search: search})
		ids := make([]string, 0, len(got))
		for _, v := range got {
			ids = append(ids, v.ID)
		}
		return ids, total, err
	})
}

func TestWorkerSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	for _, w := range []storage.Worker{
		{ID: "wk-QQAlpha", Name: "one", Host: "h1", Description: "first"},
		{ID: "wk-b", Name: "QQBravo", Host: "h2", Description: "second"},
		{ID: "wk-c", Name: "three", Host: "QQCharlie", Description: "third"},
		{ID: "wk-d", Name: "four", Host: "h4", Description: "QQDelta fourth"},
	} {
		if err := s.CreateWorker(ctx, w); err != nil {
			t.Fatalf("seed %s: %v", w.ID, err)
		}
	}

	checkAdminSearch(t, []adminCase{
		{"qqalpha", "wk-QQAlpha", "id"},
		{"QQBRAVO", "wk-b", "name"},
		{"qqcharlie", "wk-c", "host"},
		{"QQDELTA", "wk-d", "description"},
	}, func(search string) ([]string, int, error) {
		got, total, err := s.ListWorkers(ctx, storage.CommonFilter{Search: search})
		ids := make([]string, 0, len(got))
		for _, w := range got {
			ids = append(ids, w.ID)
		}
		return ids, total, err
	})
}

func TestLogSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, l := range []storage.Log{
		{ID: "lg-a", Timestamp: at, Level: "INFO", Message: "QQAlpha happened"},
		{ID: "lg-b", Timestamp: at, Level: "INFO", Message: "two", Action: "QQBravo"},
		{ID: "lg-c", Timestamp: at, Level: "INFO", Message: "three", SourceID: "QQCharlie"},
		{ID: "lg-d", Timestamp: at, Level: "INFO", Message: "four", SinkID: "QQDelta"},
		{ID: "lg-e", Timestamp: at, Level: "INFO", Message: "five", WorkflowID: "QQEcho"},
		{ID: "lg-f", Timestamp: at, Level: "INFO", Message: "six", UserID: "QQFoxtrot"},
		{ID: "lg-g", Timestamp: at, Level: "INFO", Message: "seven", Username: "QQGolf"},
	} {
		if err := s.CreateLog(ctx, l); err != nil {
			t.Fatalf("seed %s: %v", l.ID, err)
		}
	}

	checkAdminSearch(t, []adminCase{
		{"qqalpha", "lg-a", "message"},
		{"QQBRAVO", "lg-b", "action"},
		{"qqcharlie", "lg-c", "source_id"},
		{"QQDELTA", "lg-d", "sink_id"},
		{"qqecho", "lg-e", "workflow_id"},
		{"QQFOXTROT", "lg-f", "user_id"},
		{"qqgolf", "lg-g", "username"},
	}, func(search string) ([]string, int, error) {
		got, total, err := s.ListLogs(ctx, storage.LogFilter{
			CommonFilter: storage.CommonFilter{Search: search},
		})
		ids := make([]string, 0, len(got))
		for _, l := range got {
			ids = append(ids, l.ID)
		}
		return ids, total, err
	})
}

// An audit trail that cannot be searched for "DELETE" because the stored action
// says "delete" is not an audit trail anyone can use.
func TestAuditLogSearchIgnoresCaseInEveryColumnItSearches(t *testing.T) {
	s, _ := workflowStore(t, "PRAGMA case_sensitive_like = ON")
	ctx := t.Context()

	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, a := range []storage.AuditLog{
		{ID: "au-QQAlpha", Timestamp: at, Username: "one", Action: "CREATE"},
		{ID: "au-b", Timestamp: at, Username: "QQBravo", Action: "CREATE"},
		{ID: "au-c", Timestamp: at, Username: "three", Action: "QQCharlie"},
		{ID: "au-d", Timestamp: at, Username: "four", Action: "CREATE", EntityID: "QQDelta"},
		{ID: "au-e", Timestamp: at, Username: "five", Action: "CREATE", Payload: `{"k":"QQEcho"}`},
	} {
		if err := s.CreateAuditLog(ctx, a); err != nil {
			t.Fatalf("seed %s: %v", a.ID, err)
		}
	}

	checkAdminSearch(t, []adminCase{
		{"qqalpha", "au-QQAlpha", "id"},
		{"QQBRAVO", "au-b", "username"},
		{"qqcharlie", "au-c", "action"},
		{"QQDELTA", "au-d", "entity_id"},
		{"qqecho", "au-e", "payload"},
	}, func(search string) ([]string, int, error) {
		got, total, err := s.ListAuditLogs(ctx, storage.AuditFilter{
			CommonFilter: storage.CommonFilter{Search: search},
		})
		ids := make([]string, 0, len(got))
		for _, a := range got {
			ids = append(ids, a.ID)
		}
		return ids, total, err
	})
}

// Ordering. Each seeds deliberately out of creation order.

func TestListUsersReturnsNewestFirst(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{{"usr-middle", 2 * time.Hour}, {"usr-oldest", 9 * time.Hour}, {"usr-newest", 0}} {
		if err := s.CreateUser(ctx, storage.User{
			ID: seed.id, Username: seed.id, Role: storage.RoleViewer, CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, _, err := s.ListUsers(ctx, storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, u := range got {
		ids = append(ids, u.ID)
	}
	want := []string{"usr-newest", "usr-middle", "usr-oldest"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("ListUsers returned %v, want %v\nthe list carries no ORDER BY", ids, want)
	}
}

func TestListVHostsReturnsNewestFirst(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{{"vh-middle", 2 * time.Hour}, {"vh-oldest", 9 * time.Hour}, {"vh-newest", 0}} {
		if err := s.CreateVHost(ctx, storage.VHost{
			ID: seed.id, Name: seed.id, CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, _, err := s.ListVHosts(ctx, storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, v := range got {
		ids = append(ids, v.ID)
	}
	want := []string{"vh-newest", "vh-middle", "vh-oldest"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("ListVHosts returned %v, want %v\nthe list carries no ORDER BY", ids, want)
	}
}

func TestListWorkersReturnsNewestFirst(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{{"wk-middle", 2 * time.Hour}, {"wk-oldest", 9 * time.Hour}, {"wk-newest", 0}} {
		if err := s.CreateWorker(ctx, storage.Worker{
			ID: seed.id, Name: seed.id, CreatedAt: base.Add(-seed.age),
		}); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, _, err := s.ListWorkers(ctx, storage.CommonFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := make([]string, 0, len(got))
	for _, w := range got {
		ids = append(ids, w.ID)
	}
	want := []string{"wk-newest", "wk-middle", "wk-oldest"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("ListWorkers returned %v, want %v\nthe list carries no ORDER BY", ids, want)
	}
}

// Plugins have no Create on the Storage interface — Init seeds the catalogue —
// so these rows go in directly, and the assertion is on the relative order of
// the three among whatever else Init put there.
func TestListPluginsReturnsNewestFirst(t *testing.T) {
	s, db := workflowStore(t)
	ctx := t.Context()

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	mine := map[string]bool{"pl-middle": true, "pl-oldest": true, "pl-newest": true}
	for _, seed := range []struct {
		id  string
		age time.Duration
	}{{"pl-middle", 2 * time.Hour}, {"pl-oldest", 9 * time.Hour}, {"pl-newest", 0}} {
		// Every text column gets a value: ListPlugins scans them into plain
		// strings, so a NULL is a scan error rather than an empty field.
		if _, err := db.ExecContext(ctx,
			"INSERT INTO plugins (id, name, description, author, stars, category, certified, type, wasm_url, installed, created_at) "+
				"VALUES (?, ?, '', '', 0, '', 0, 'WASM', '', 0, ?)",
			seed.id, seed.id, base.Add(-seed.age)); err != nil {
			t.Fatalf("seed %s: %v", seed.id, err)
		}
	}

	got, err := s.ListPlugins(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []string
	for _, p := range got {
		if mine[p.ID] {
			ids = append(ids, p.ID)
		}
	}
	want := []string{"pl-newest", "pl-middle", "pl-oldest"}
	if fmt.Sprint(ids) != fmt.Sprint(want) {
		t.Errorf("ListPlugins returned %v of the seeded rows, want %v\n"+
			"the list carries no ORDER BY", ids, want)
	}
}

func TestCreateStampsTheCreationTimeForUsersVHostsAndWorkers(t *testing.T) {
	s, _ := workflowStore(t)
	ctx := t.Context()

	before := time.Now().Add(-time.Second)
	if err := s.CreateUser(ctx, storage.User{ID: "usr-1", Username: "u", Role: storage.RoleViewer}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-1", Name: "v"}); err != nil {
		t.Fatalf("create vhost: %v", err)
	}
	if err := s.CreateWorker(ctx, storage.Worker{ID: "wk-1", Name: "w"}); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	after := time.Now().Add(time.Second)

	inWindow := func(what string, got time.Time) {
		t.Helper()
		if got.IsZero() || got.Before(before) || got.After(after) {
			t.Errorf("%s CreatedAt is %v, want a time inside %v..%v", what, got, before, after)
		}
	}

	u, err := s.GetUser(ctx, "usr-1")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	inWindow("user", u.CreatedAt)

	v, err := s.GetVHost(ctx, "vh-1")
	if err != nil {
		t.Fatalf("get vhost: %v", err)
	}
	inWindow("vhost", v.CreatedAt)

	w, err := s.GetWorker(ctx, "wk-1")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	inWindow("worker", w.CreatedAt)
}

func TestInitBackfillsAdminTablesWithNoCreationTime(t *testing.T) {
	s, db := workflowStore(t)
	ctx := t.Context()

	if err := s.CreateUser(ctx, storage.User{ID: "usr-old", Username: "u", Role: storage.RoleViewer}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := s.CreateVHost(ctx, storage.VHost{ID: "vh-old", Name: "v"}); err != nil {
		t.Fatalf("seed vhost: %v", err)
	}
	if err := s.CreateWorker(ctx, storage.Worker{ID: "wk-old", Name: "w"}); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
	for _, table := range []string{"users", "vhosts", "workers", "plugins"} {
		if _, err := db.ExecContext(ctx, "UPDATE "+table+" SET created_at = NULL"); err != nil {
			t.Fatalf("blank %s.created_at: %v", table, err)
		}
	}

	if err := s.Init(ctx); err != nil {
		t.Fatalf("re-init: %v", err)
	}

	u, err := s.GetUser(ctx, "usr-old")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.CreatedAt.IsZero() {
		t.Error("a user left with a NULL created_at by the migration still has one after Init")
	}
	v, err := s.GetVHost(ctx, "vh-old")
	if err != nil {
		t.Fatalf("get vhost: %v", err)
	}
	if v.CreatedAt.IsZero() {
		t.Error("a vhost left with a NULL created_at by the migration still has one after Init")
	}
	w, err := s.GetWorker(ctx, "wk-old")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if w.CreatedAt.IsZero() {
		t.Error("a worker left with a NULL created_at by the migration still has one after Init")
	}

	var nullPlugins int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM plugins WHERE created_at IS NULL").Scan(&nullPlugins); err != nil {
		t.Fatalf("count plugins: %v", err)
	}
	if nullPlugins != 0 {
		t.Errorf("%d plugins still have a NULL created_at after Init", nullPlugins)
	}
}

// The admin lists against real PostgreSQL.
//
// The cases above run on sqlite with case_sensitive_like, which reproduces the
// collation behaviour but not the rest of the dialect: the new column is added
// by autoMigrate rather than CREATE TABLE on an existing database, and the
// ORDER BY travels through placeholder rewriting. Neither is exercised above.
func TestAdminListsAndSearchOnPostgres(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if os.Getenv("HERMOD_INTEGRATION") != "1" || dsn == "" {
		t.Skip("integration: set HERMOD_INTEGRATION=1 and POSTGRES_DSN to enable")
	}

	ctx := t.Context()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be opened (%s): %v", dsn, err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := NewSQLStorage(db, "pgx")
	if err := s.Init(ctx); err != nil {
		t.Fatalf("POSTGRES_DSN names a server that could not be initialised (%s): %v", dsn, err)
	}

	// users.username and vhosts.name are UNIQUE, so the token has to make every
	// seeded row distinct as well as scope the search to this run.
	token := fmt.Sprintf("adm%d", time.Now().UnixNano())
	base := time.Now().UTC().Truncate(time.Second)
	ages := []struct {
		suffix string
		age    time.Duration
	}{{"mid", 2 * time.Hour}, {"old", 9 * time.Hour}, {"new", 0}}

	for _, a := range ages {
		id := token + "-" + a.suffix
		at := base.Add(-a.age)
		if err := s.CreateUser(ctx, storage.User{
			ID: id, Username: id, Role: storage.RoleViewer, CreatedAt: at,
		}); err != nil {
			t.Fatalf("seed user %s: %v", id, err)
		}
		if err := s.CreateVHost(ctx, storage.VHost{ID: id, Name: id, CreatedAt: at}); err != nil {
			t.Fatalf("seed vhost %s: %v", id, err)
		}
		if err := s.CreateWorker(ctx, storage.Worker{ID: id, Name: id, CreatedAt: at}); err != nil {
			t.Fatalf("seed worker %s: %v", id, err)
		}
	}
	t.Cleanup(func() {
		for _, a := range ages {
			id := token + "-" + a.suffix
			for _, table := range []string{"users", "vhosts", "workers"} {
				_, _ = db.ExecContext(context.Background(),
					fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, table), id)
			}
		}
	})

	want := []string{token + "-new", token + "-mid", token + "-old"}
	// The token is lower-case in every stored value, so the upper-case search is
	// the shape that returned nothing before.
	for _, search := range []string{token, strings.ToUpper(token)} {
		for _, list := range []struct {
			name string
			ids  func() ([]string, int, error)
		}{
			{"ListUsers", func() ([]string, int, error) {
				got, total, err := s.ListUsers(ctx, storage.CommonFilter{Search: search})
				ids := make([]string, 0, len(got))
				for _, u := range got {
					ids = append(ids, u.ID)
				}
				return ids, total, err
			}},
			{"ListVHosts", func() ([]string, int, error) {
				got, total, err := s.ListVHosts(ctx, storage.CommonFilter{Search: search})
				ids := make([]string, 0, len(got))
				for _, v := range got {
					ids = append(ids, v.ID)
				}
				return ids, total, err
			}},
			{"ListWorkers", func() ([]string, int, error) {
				got, total, err := s.ListWorkers(ctx, storage.CommonFilter{Search: search})
				ids := make([]string, 0, len(got))
				for _, w := range got {
					ids = append(ids, w.ID)
				}
				return ids, total, err
			}},
		} {
			ids, total, err := list.ids()
			if err != nil {
				t.Errorf("%s searching for %q failed: %v", list.name, search, err)
				continue
			}
			if total != 3 {
				t.Errorf("%s searching for %q reported total %d, want 3\n"+
					"PostgreSQL's LIKE folds no case", list.name, search, total)
				continue
			}
			if fmt.Sprint(ids) != fmt.Sprint(want) {
				t.Errorf("%s searching for %q returned %v, want %v", list.name, search, ids, want)
			}
		}
	}
}
