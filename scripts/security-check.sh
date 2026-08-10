#!/usr/bin/env bash
#
# Security verification.
#
# SECURITY.md makes a set of claims about how Hermod behaves. This runs the
# checks that hold them, in one command, so the posture is verified rather than
# remembered — and so a claim that quietly stopped being true fails here instead
# of during an incident.
#
# Every check below maps to a claim in SECURITY.md, and every claim in
# SECURITY.md that *can* be checked maps to one here. Where a claim has no
# check, that is stated rather than left implicit; see "Not covered" at the end
# of this file.
#
#   ./scripts/security-check.sh          # everything that needs no server
#   ./scripts/security-check.sh --e2e    # also the browser checks (needs a
#                                        # running stack; see below)
#
# The browser checks need a stack:
#   ./scripts/dev.sh --sqlite
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

RUN_E2E=0
[[ "${1:-}" == "--e2e" ]] && RUN_E2E=1

# golangci-lint and govulncheck install to GOPATH/bin, which is not always on
# PATH — and a check that silently did not run reads exactly like one that
# passed.
export PATH="$(go env GOPATH)/bin:$PATH"

BOLD=$'\033[1m'; GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; RESET=$'\033[0m'
FAILED=()

step() { printf '\n%s▸ %s%s\n' "$BOLD" "$1" "$RESET"; }
note() { printf '  %s%s%s\n' "$DIM" "$1" "$RESET"; }

run() {
  local name="$1"; shift
  if "$@" >/tmp/seccheck.out 2>&1; then
    printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$name"
  else
    printf '  %s✗%s %s\n' "$RED" "$RESET" "$name"
    sed 's/^/      /' /tmp/seccheck.out | tail -25
    FAILED+=("$name")
  fi
}

step "Dependency vulnerabilities"
note "Claim: no reachable known vulnerability; exemptions are reviewed and justified."
run "govulncheck (with reviewed exemptions)" ./scripts/govulncheck.sh

step "Authentication and session handling"
note "Claims: the session token is unreachable from JavaScript; streams authenticate"
note "by cookie and refuse a token in the URL; logout ends the session; the window"
note "is an hour with an absolute cap."
run "stream auth rules"      go test ./internal/api/handlers/ -run 'UIStreams|IntegrationWSEndpoints|NormalAPIUnaffected|ExpiredSession' -count=1
run "session lifetime"       go test ./internal/api/handlers/ -run 'Session|Renew' -count=1

step "CSRF"
note "Claim: cookie-authenticated state changes require a matching double-submit token,"
note "and header-authenticated requests are deliberately exempt."
run "CSRF enforcement and exemptions" go test ./internal/api/handlers/ -run 'CSRF|CookieAuthStateChange|HeaderAuthIsExempt|WorkerTokenIsExempt|PublicEndpointsAreNot|TokenComparison' -count=1

step "Authorization"
note "Claim: every state-changing route is behind an authorization guard, or listed"
note "with the reason it is not."
run "route guard census" go test ./internal/api/handlers/ -run 'TestEveryMutatingRouteIsGuarded' -count=1
run "RBAC"               go test ./internal/api/handlers/ -run 'Rbac|RBAC' -count=1

step "Response headers"
note "Claim: a restrictive CSP is set, production drops inline styles, and the policy"
note "still permits the WebSockets the live views need."
run "security headers and CSP" go test ./internal/api/handlers/ -run 'SecurityHeaders|CSP' -count=1

step "Input handling"
note "Claims: SQL identifiers are validated and quoted rather than interpolated;"
note "webhook signatures, plugin IDs, WASM URLs and CORS origins are checked."
run "identifier quoting and validation" go test ./pkg/infra/sqlutil/ -count=1
run "signature, path and origin checks" go test ./internal/api/handlers/ -run 'VerifyWebhookSignature|ValidatePluginID|IsSafeWasmURL|IsValidFormPath|CORS|Origin' -count=1

step "Serialisation exposure"
note "Claim: Hermod never decodes untrusted Avro, which is why three advisories in"
note "hamba/avro are accepted rather than blocking."
run "no Avro decode entry points" go test ./pkg/infra/schema/ -run 'TestNoUntrustedAvroDecoding' -count=1

step "Static analysis"
note "gosec and the rest of the security linters, on new code."
run "golangci-lint" golangci-lint run ./...

if [[ "$RUN_E2E" == "1" ]]; then
  step "Browser verification"
  note "Claims that can only be checked in a real browser: what the client stores,"
  note "and what it sends on a WebSocket handshake."
  run "no credential in web storage" bunx playwright test ui/__tests__/no_token_in_storage_e2e.spec.ts --reporter=line
  run "streams carry no URL credential" bunx playwright test ui/__tests__/stream_auth_e2e.spec.ts --reporter=line
else
  printf '\n%s▸ Browser verification%s\n' "$BOLD" "$RESET"
  note "skipped — re-run with --e2e against a running stack (./scripts/dev.sh --sqlite)"
  note "Until then, two claims are unverified: that no credential reaches web storage,"
  note "and that streams send none in the URL."
fi

# --- Not covered ---------------------------------------------------------------
#
# Stated so the list above is not mistaken for the whole posture:
#
#   * Session revocation. The session is a stateless JWT, so a captured token
#     stays valid until it expires. The window is bounded (an hour, capped at a
#     day) but not revocable. There is nothing to check because it is not
#     implemented; SECURITY.md tracks it.
#   * "Do not log secrets or PII." No test holds this — it is a review
#     discipline, and a grep would be theatre rather than a check.
#   * The two-phase-commit operational hazard. Covered by the integration tests
#     in pkg/comm/sink/txgroup, which need a real PostgreSQL and so are not run
#     here. See the README's operational hazard section.

printf '\n'
if [[ ${#FAILED[@]} -eq 0 ]]; then
  printf '%s✓ every security check passed%s\n' "$GREEN$BOLD" "$RESET"
  exit 0
fi

printf '%s✗ %d check(s) failed:%s\n' "$RED$BOLD" "${#FAILED[@]}" "$RESET"
printf '    %s\n' "${FAILED[@]}"
exit 1
