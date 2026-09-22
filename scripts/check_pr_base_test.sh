#!/usr/bin/env bash
#
# Verifies scripts/check_pr_base.sh refuses the merge shape that has silently
# dropped four fixes on the floor.
#
#   ./scripts/check_pr_base_test.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$REPO_ROOT/scripts/check_pr_base.sh"

if [[ -t 1 ]]; then
  GREEN=$'\033[32m'; RED=$'\033[31m'; RESET=$'\033[0m'
else
  GREEN=""; RED=""; RESET=""
fi

FAILURES=0
pass() { echo "  ${GREEN}✓${RESET} $*"; }
fail() { echo "  ${RED}✗${RESET} $*" >&2; FAILURES=$((FAILURES + 1)); }

# run <expected-exit> <description> <env assignments...>
run() {
  local want="$1" desc="$2"; shift 2
  local out status
  out="$(env "$@" "$CHECK" 2>&1)" && status=0 || status=$?
  if [[ "$status" -eq "$want" ]]; then
    pass "$desc"
  else
    fail "$desc (exit $status, wanted $want)"
    echo "      ${out//$'\n'/$'\n      '}" >&2
  fi
}

echo "check_pr_base.sh"

# The ordinary case: everything targets the default branch.
run 0 "base main passes" \
  PR_BASE=main PR_LABELS=""

# The precondition for every orphaned merge. Without the label this is refused
# outright, which is the whole point: no non-main base, no trap.
run 1 "base on another branch fails without the label" \
  PR_BASE=fix/tomap-snapshot-aliasing PR_LABELS=""

# A deliberate stack, declared, whose base has not landed yet. Allowed, because
# the reviewer has said they know and the base is still mergeable.
run 0 "labelled stack passes while its base PR is open" \
  PR_BASE=fix/tomap-snapshot-aliasing PR_LABELS=stacked BASE_PR_STATE=OPEN

# The exact shape of #101, #148, #150 and #160: the base already merged, so this
# PR would merge into an orphaned branch and report success without reaching main.
run 1 "labelled stack fails once its base PR has merged" \
  PR_BASE=fix/tomap-snapshot-aliasing PR_LABELS=stacked BASE_PR_STATE=MERGED

run 1 "labelled stack fails when its base PR was closed" \
  PR_BASE=fix/tomap-snapshot-aliasing PR_LABELS=stacked BASE_PR_STATE=CLOSED

# A base branch with no PR at all cannot be shown to be live, so it is refused
# rather than assumed safe.
run 1 "labelled stack fails when the base has no PR" \
  PR_BASE=some/loose-branch PR_LABELS=stacked BASE_PR_STATE=NONE

# The label is matched exactly: a substring must not smuggle an exemption in.
run 1 "a label merely containing the word does not exempt" \
  PR_BASE=fix/whatever PR_LABELS=unstackable BASE_PR_STATE=OPEN

# Labels arrive comma-joined, so the match has to work at any position.
run 0 "the label is found among others" \
  PR_BASE=fix/whatever PR_LABELS="needs-review,stacked,perf" BASE_PR_STATE=OPEN

# A repository whose default branch is not called main still works.
run 0 "the default branch is configurable" \
  PR_BASE=trunk PR_LABELS="" DEFAULT_BRANCH=trunk

# Missing input is a broken workflow, not a passing check.
run 2 "an empty base is an error rather than a pass" \
  PR_BASE="" PR_LABELS=""

if [[ "$FAILURES" -gt 0 ]]; then
  echo "${RED}$FAILURES check(s) failed${RESET}" >&2
  exit 1
fi
echo "${GREEN}all checks passed${RESET}"
