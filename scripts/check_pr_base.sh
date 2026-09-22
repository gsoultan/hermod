#!/usr/bin/env bash
#
# Refuses a pull request that would merge somewhere other than the default
# branch, unless the stack is declared and its base is still live.
#
# Why this is a gate and not advice. Every PR here squash-merges, which means a
# branch's content reaches main as a *new* commit. Stack PR B on PR A's branch
# and the two merges race: if A lands first, A's branch is orphaned, and B then
# merges into that orphan. GitHub reports B as MERGED, it drops off the open-PR
# list, and its content is not in main. Ancestry cannot tell you — a squash
# leaves no parent to follow.
#
# That is not hypothetical. It has happened four times:
#
#   #101 feat/fcm-sink                    merged 10s after its base landed
#   #148 fix/jsonb-column-shape…          merged 1h24m after
#   #150 fix/trace-retention-per-workflow merged 45m after
#   #160 fix/metadata-ref-unlocked-reads  merged 11s after
#
# Each was caught and re-landed by hand (#102, #154, #152, #161). One of them
# left a `concurrent map read and map write` crash live in main in the meantime,
# and another left a trace-retention bug that drops another workflow's
# partitions. Every one looked shipped.
#
# The trap has one precondition: a base that is not the default branch. So that
# is what this refuses. A genuine stack — the second change cannot compile
# without the first — is still possible with the `stacked` label, and then this
# checks the thing that actually matters: that the base's own PR is still open.
# Once the base has merged, a stacked PR is already in the losing position and
# must be retargeted before it can merge.
#
# Note what this cannot do. No pull_request event fires when some *other* branch
# merges, so this cannot re-run at the moment a base is orphaned. Refusing the
# precondition is what makes it reliable rather than best-effort.
#
#   PR_BASE=main PR_LABELS="" ./scripts/check_pr_base.sh
#
# Inputs, all from the workflow:
#   PR_BASE        the pull request's base branch            (required)
#   PR_LABELS      its labels, comma-separated               (optional)
#   DEFAULT_BRANCH the branch everything should target       (default: main)
#   BASE_PR_STATE  OPEN|MERGED|CLOSED|NONE for the base's own PR. Looked up with
#                  gh when unset; set directly by the tests, which is the only
#                  seam they need.

set -euo pipefail

PR_BASE="${PR_BASE-}"
PR_LABELS="${PR_LABELS-}"
DEFAULT_BRANCH="${DEFAULT_BRANCH:-main}"
STACK_LABEL="stacked"

if [[ -z "$PR_BASE" ]]; then
  echo "check_pr_base: PR_BASE is empty — the workflow is not passing the base branch." >&2
  exit 2
fi

# The ordinary case, and the one that needs no thought.
if [[ "$PR_BASE" == "$DEFAULT_BRANCH" ]]; then
  echo "Base is $DEFAULT_BRANCH."
  exit 0
fi

# Exact match against one comma-separated entry. A substring must not exempt
# anything: "unstackable" is not consent.
has_label() {
  local want="$1" label
  local IFS=,
  for label in $PR_LABELS; do
    # Trim the spaces a hand-written label list picks up.
    label="${label#"${label%%[![:space:]]*}"}"
    label="${label%"${label##*[![:space:]]}"}"
    [[ "$label" == "$want" ]] && return 0
  done
  return 1
}

if ! has_label "$STACK_LABEL"; then
  cat >&2 <<EOF
This pull request targets '$PR_BASE', not '$DEFAULT_BRANCH'.

Every PR here squash-merges, so if '$PR_BASE' lands first this PR merges into an
orphaned branch: GitHub marks it MERGED, it leaves the open-PR list, and its
content never reaches '$DEFAULT_BRANCH'. That has happened four times in this
repository (#101, #148, #150, #160) and each one had to be re-landed by hand.

Retarget this PR at '$DEFAULT_BRANCH'. Two PRs off the default branch that share
no code are safer than a stack even when the second logically follows the first
— a CHANGELOG conflict on whichever merges second is a far cheaper problem than
a silently unshipped fix.

If the stack is genuinely unavoidable — this change cannot compile without the
other — add the '$STACK_LABEL' label. That is a statement that you will retarget
before merging, and this check will then hold you to the base still being open.
EOF
  exit 1
fi

# A declared stack. The remaining question is whether it is still in a position
# to merge anywhere useful.
if [[ -z "${BASE_PR_STATE-}" ]]; then
  BASE_PR_STATE="$(
    gh pr list --head "$PR_BASE" --state all \
      --json state --jq '.[0].state // "NONE"' 2>/dev/null || echo "NONE"
  )"
fi

case "$BASE_PR_STATE" in
  OPEN)
    echo "Base '$PR_BASE' is a declared stack and its PR is still open."
    echo "Retarget this PR at '$DEFAULT_BRANCH' before merging it."
    exit 0
    ;;
  NONE)
    echo "Base '$PR_BASE' has no pull request, so there is no way to tell whether" >&2
    echo "it will ever reach '$DEFAULT_BRANCH'. Retarget this PR at '$DEFAULT_BRANCH'." >&2
    exit 1
    ;;
  *)
    cat >&2 <<EOF
Base '$PR_BASE' is already $BASE_PR_STATE.

Merging this pull request now would put its content into a branch that is no
longer going anywhere. It would be reported as MERGED and would not be in
'$DEFAULT_BRANCH' — the failure this check exists for, four times over.

Retarget this PR at '$DEFAULT_BRANCH'. If it no longer applies cleanly,
cherry-pick the commit onto a fresh branch off '$DEFAULT_BRANCH': a squash
preserves the branch tip's tree, so this is normally clean.
EOF
    exit 1
    ;;
esac
