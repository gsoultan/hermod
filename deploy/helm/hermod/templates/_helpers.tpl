{{- define "hermod.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "hermod.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "hermod.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "hermod.labels" -}}
helm.sh/chart: {{ include "hermod.chart" . }}
{{ include "hermod.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "hermod.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hermod.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
The ML worker's labels. Its name label differs from Hermod's, so Hermod's
Service never selects the worker's pods, nor the worker's Service Hermod's.
*/}}
{{- define "hermod.mlWorker.fullname" -}}
{{- printf "%s-ml" (include "hermod.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "hermod.mlWorker.selectorLabels" -}}
app.kubernetes.io/name: {{ printf "%s-ml" (include "hermod.name" .) }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: ml-worker
{{- end }}

{{- define "hermod.mlWorker.labels" -}}
helm.sh/chart: {{ include "hermod.chart" . }}
{{ include "hermod.mlWorker.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "hermod.mlWorker.secretName" -}}
{{- .Values.mlWorker.existingSecret | default (include "hermod.mlWorker.fullname" .) }}
{{- end }}

{{/*
The ML worker's image tag: mlWorker.image.tag when set, else the chart's
appVersion, with "-dl" appended for the deep-learning variant.
*/}}
{{- define "hermod.mlWorker.imageTag" -}}
{{- $image := .Values.mlWorker.image -}}
{{- $variant := $image.variant | default "" -}}
{{- if not (has $variant (list "" "dl")) -}}
{{- fail (printf "mlWorker.image.variant %q must be empty or \"dl\"" $variant) -}}
{{- end -}}
{{- if $image.tag -}}
{{- $image.tag -}}
{{- else if eq $variant "dl" -}}
{{- printf "%s-dl" .Chart.AppVersion -}}
{{- else -}}
{{- .Chart.AppVersion -}}
{{- end -}}
{{- end }}

{{- define "hermod.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "hermod.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "hermod.secretName" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecret }}
{{- else }}
{{- include "hermod.fullname" . }}
{{- end }}
{{- end }}

{{/*
hermod.shutdownSeconds parses shutdownTimeout ("25s", "1m") into seconds so it
can be compared against terminationGracePeriodSeconds. Helm has no duration
parser, so this handles the two suffixes the value realistically takes and
refuses anything else rather than guessing.
*/}}
{{- define "hermod.shutdownSeconds" -}}
{{- $v := .Values.shutdownTimeout | toString -}}
{{- if hasSuffix "ms" $v -}}
{{- div (trimSuffix "ms" $v | int) 1000 -}}
{{- else if hasSuffix "s" $v -}}
{{- trimSuffix "s" $v | int -}}
{{- else if hasSuffix "m" $v -}}
{{- mul (trimSuffix "m" $v | int) 60 -}}
{{- else -}}
{{- fail (printf "shutdownTimeout %q must end in ms, s or m" $v) -}}
{{- end -}}
{{- end }}

{{/*
hermod.validate fails rendering on configurations that deploy but lose data.
Catching these at `helm template` is the whole point — the alternative is
finding out during a rolling restart.
*/}}
{{- define "hermod.validate" -}}
{{- $shutdown := include "hermod.shutdownSeconds" . | int -}}
{{- $grace := .Values.terminationGracePeriodSeconds | int -}}
{{- if le $grace $shutdown -}}
{{- fail (printf "terminationGracePeriodSeconds (%d) must exceed shutdownTimeout (%ds), or Kubernetes kills the pod mid-drain and every message it had taken from a source but not yet written is discarded" $grace $shutdown) -}}
{{- end -}}
{{- if and .Values.masterKey .Values.existingSecret -}}
{{- fail "set either masterKey or existingSecret, not both: it is ambiguous which key encrypts stored credentials" -}}
{{- end -}}
{{- if and .Values.mlWorker.enabled (not .Values.mlWorker.token) (not .Values.mlWorker.existingSecret) -}}
{{- fail "mlWorker needs a token (mlWorker.token or mlWorker.existingSecret): it holds datasets and model files, and anything that can reach it could read or replace them" -}}
{{- end -}}
{{- if and .Values.mlWorker.token .Values.mlWorker.existingSecret -}}
{{- fail "set either mlWorker.token or mlWorker.existingSecret, not both" -}}
{{- end -}}
{{- range $pool := list "customPool" "gpuPool" -}}
{{- $p := index $.Values.mlWorker $pool -}}
{{- if $p.enabled -}}
{{- if not $.Values.mlWorker.enabled -}}
{{- fail (printf "mlWorker.%s needs mlWorker.enabled: the main worker holds the datasets the pool trains on and serves the models it makes" $pool) -}}
{{- end -}}
{{- if and (not $p.token) (not $p.existingSecret) -}}
{{- fail (printf "mlWorker.%s needs a token of its own (mlWorker.%s.token or existingSecret)" $pool $pool) -}}
{{- end -}}
{{- if and $p.token $p.existingSecret -}}
{{- fail (printf "set either mlWorker.%s.token or mlWorker.%s.existingSecret, not both" $pool $pool) -}}
{{- end -}}
{{- if and $p.token (eq $p.token $.Values.mlWorker.token) -}}
{{- fail (printf "mlWorker.%s.token must differ from mlWorker.token: the main worker's token opens every vhost's datasets and models" $pool) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if .Values.mlWorker.customPool.enabled -}}
{{- if ne (int .Values.mlWorker.customPool.maxTrainings) 1 -}}
{{- fail "mlWorker.customPool.maxTrainings must be 1: a custom script must not share its pod with another training's data" -}}
{{- end -}}
{{- if not .Values.mlWorker.customPool.networkPolicy.enabled -}}
{{- fail "mlWorker.customPool.networkPolicy.enabled cannot be false: the pool runs uploaded code, and its NetworkPolicy is what keeps it from reaching the network" -}}
{{- end -}}
{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) (not .Values.database.type) (not .Values.database.connectionSecret) -}}
{{- fail "replicaCount > 1 needs a shared database: with the on-disk default each replica keeps its own workflows, leases and users, so they cannot coordinate" -}}
{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) .Values.persistence.enabled (has "ReadWriteOnce" .Values.persistence.accessModes) -}}
{{- fail "replicaCount > 1 with a ReadWriteOnce volume will leave replicas after the first unschedulable; use ReadWriteMany or disable persistence and rely on the shared database" -}}
{{- end -}}
{{- end }}
