#!/usr/bin/env bash
# Turn a rendered PrometheusRule into the bare rule file promtool understands.
#
# promtool validates rule *files* — a top-level `groups:` — not the Kubernetes
# custom resource that wraps them. Everything under the PrometheusRule's `spec:`
# is the rule file, indented by two spaces, so this strips the wrapper and the
# indent.
#
# The document scoping matters: `helm template` emits every manifest in the
# chart, and a Deployment, Service and PVC all have a `spec:` too. Taking the
# first one found produces a file that fails validation for reasons that have
# nothing to do with the alerts.
#
# Usage:
#   helm template t deploy/helm/hermod --set metrics.prometheusRule.enabled=true \
#     | scripts/extract-prometheus-rules.sh > rules.yaml
#   promtool check rules rules.yaml
set -euo pipefail

awk '
  # Document boundary: forget whatever the previous manifest was.
  /^---[[:space:]]*$/ { inrule = 0; inspec = 0; next }

  # Only the PrometheusRule is of interest.
  /^kind:[[:space:]]*PrometheusRule[[:space:]]*$/ { inrule = 1; next }

  inrule && /^spec:[[:space:]]*$/ { inspec = 1; next }

  inspec {
    # A line at column 0 ends the spec block.
    if ($0 !~ /^[[:space:]]/ && $0 != "") { inspec = 0; inrule = 0; next }
    # Drop exactly the two spaces that nest the rule file under spec.
    sub(/^  /, "")
    print
  }
'
