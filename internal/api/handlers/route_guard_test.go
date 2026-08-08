package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Every mutating route must be behind an authorization guard.
//
// RbacMiddleware is well tested now, but a guard only protects the routes it is
// actually attached to. The realistic failure is not the middleware being
// wrong — it is someone adding
//
//	mux.HandleFunc("POST /api/things", h.CreateThing)
//
// next to fifty correctly-guarded siblings and nobody noticing that this one
// says HandleFunc instead of Handle(h.EditorOnly(...)). Reviewing that by eye
// across a dozen files does not scale, so it is checked mechanically.
//
// Every /api/ route is already *authenticated* — AuthMiddleware wraps the whole
// mux and only lets an explicit allowlist through. What this checks is
// *authorization*: whether a state-changing route restricts which role may call
// it. Without a wrapper, a Viewer — the read-only role — could delete the log
// store, register schemas and create, rename or delete workers.
//
// The rule: a route that changes state (POST, PUT, PATCH, DELETE) must either
// be wrapped in an authorization guard, or appear below with the reason it is
// not. GET routes are read paths and out of scope.
// ---------------------------------------------------------------------------

// publicMutatingRoutes are the state-changing routes that are unauthenticated
// on purpose. Each needs a reason, and the list is deliberately short: adding
// to it is a security decision, not a formality.
// unguardedMutatingRoutes are the state-changing routes with no route-level
// guard, each with the reason. Three kinds are legitimate:
//
//   - pre-auth: the request is what establishes the session, so it cannot
//     require one;
//   - self-service: the handler restricts the action to the caller's own
//     record, which a role wrapper cannot express;
//   - agent: worker processes call it with their own credentials, so a
//     human-role wrapper would break the data plane.
//
// Anything else is a gap. Adding an entry here is a security decision.
var unguardedMutatingRoutes = map[string]string{
	// pre-auth
	"POST /api/login":                   "issues the session; cannot require one",
	"POST /api/auth/2fa/login":          "second factor of that same login",
	"POST /api/auth/2fa/setup/pending":  "enrolment during login, before a session exists",
	"POST /api/auth/2fa/verify/pending": "enrolment during login, before a session exists",
	"POST /api/forgot-password":         "unauthenticated by nature",
	"POST /api/config/setup":            "first-run wizard; no admin exists yet to authorize it",
	"POST /api/config/database":         "first-run wizard; guarded by IsFirstRun instead",
	"POST /api/webhooks/{path...}":      "public ingestion endpoint; authenticated by its own token",
	"POST /api/forms/{path...}":         "public form submission",

	// self-service: the handler checks caller identity, not role
	"PUT /api/me":                      "a user editing their own profile",
	"PUT /api/users/{id}/password":     "handler allows administrators or the user themselves",
	"POST /api/auth/2fa/setup":         "enrolling the caller's own second factor",
	"POST /api/auth/2fa/verify":        "verifying the caller's own second factor",
	"POST /api/auth/2fa/disable":       "disabling the caller's own second factor",
	"POST /api/auth/generate-password": "returns a random string; touches no state",

	// agent paths: called by worker processes, not humans
	"POST /api/logs":                   "worker agents ship logs here (worker_api_client.go)",
	"POST /api/logs/batch":             "worker agents ship batched logs here",
	"POST /api/workers/{id}/heartbeat": "worker agents report liveness here",
	"PATCH /api/workflows/{id}/status": "worker agents report workflow status",
	"PATCH /api/workflows/{id}/stats":  "worker agents report throughput counters",

	// in-handler role checks (see the named handler)
	"POST /api/workers/{id}/start":                  "StartWorker checks RoleAdministrator itself",
	"POST /api/workers/{id}/shutdown":               "ShutdownWorker checks RoleAdministrator itself",
	"PUT /api/config/crypto":                        "infra handlers check RoleAdministrator themselves",
	"PUT /api/config/secrets":                       "infra handlers check RoleAdministrator themselves",
	"PUT /api/config/state":                         "infra handlers check RoleAdministrator themselves",
	"PUT /api/config/storage":                       "infra handlers check RoleAdministrator themselves",
	"PUT /api/config/observability":                 "infra handlers check RoleAdministrator themselves",
	"POST /api/config/databases":                    "infra handlers check RoleAdministrator themselves",
	"POST /api/config/database/test":                "infra handlers check RoleAdministrator themselves",
	"PUT /api/settings":                             "infra handlers check RoleAdministrator themselves",
	"POST /api/settings/test":                       "infra handlers check RoleAdministrator themselves",
	"POST /api/settings/test-config":                "infra handlers check RoleAdministrator themselves",
	"POST /api/backup/import":                       "infra handlers check RoleAdministrator themselves",
	"POST /api/mesh/clusters":                       "infra handlers check RoleAdministrator themselves",
	"POST /api/utils/token":                         "infra handlers check RoleAdministrator themselves",
	"POST /api/graphql/{path...}":                   "proxies to the GraphQL handler, which applies its own auth",
	"POST /api/workflows/{id}/nodes/{node_id}/test": "read-only node simulation; runs nothing persistent",
}

// routeRegistration matches mux.HandleFunc("POST /api/x", ...) and
// mux.Handle("POST /api/x", ...).
var routeRe = regexp.MustCompile(`mux\.(Handle|HandleFunc)\(\s*"([A-Z]+)\s+([^"]+)"\s*,\s*([^\n]*)`)

func TestEveryMutatingRouteIsGuarded(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	var unguarded []string
	seen := 0

	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)

		for _, m := range routeRe.FindAllStringSubmatch(string(src), -1) {
			method, route, handler := m[2], m[3], m[4]
			switch method {
			case "GET", "HEAD", "OPTIONS":
				continue // read paths
			}
			seen++

			key := method + " " + route
			if _, public := unguardedMutatingRoutes[key]; public {
				continue
			}
			// Guarded if the handler expression runs through an authorization
			// wrapper.
			if strings.Contains(handler, "EditorOnly") ||
				strings.Contains(handler, "AdminOnly") ||
				strings.Contains(handler, "RbacMiddleware") {
				continue
			}
			unguarded = append(unguarded, fmt.Sprintf("%s  (%s)", key, rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	if seen == 0 {
		t.Fatal("found no mutating routes; the scanner is broken, not the code")
	}
	t.Logf("checked %d mutating routes", seen)

	if len(unguarded) > 0 {
		sort.Strings(unguarded)
		t.Errorf("%d mutating route(s) are registered without an authorization guard.\n"+
			"Wrap them in h.EditorOnly / h.AdminOnly, or — if the route is genuinely public —\n"+
			"add it to unguardedMutatingRoutes with the reason:\n  %s",
			len(unguarded), strings.Join(unguarded, "\n  "))
	}
}

// TestUnguardedRouteListIsHonest keeps the exemption list from rotting
// into a place where guards go to be forgotten: every entry must correspond to
// a route that actually exists.
func TestUnguardedRouteListIsHonest(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolving repo root: %v", err)
	}

	registered := map[string]bool{}
	err = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range routeRe.FindAllStringSubmatch(string(src), -1) {
			registered[m[2]+" "+m[3]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}

	for route := range unguardedMutatingRoutes {
		if !registered[route] {
			t.Errorf("unguardedMutatingRoutes lists %q, which is not registered anywhere; "+
				"stale exemptions hide real gaps", route)
		}
	}
}
