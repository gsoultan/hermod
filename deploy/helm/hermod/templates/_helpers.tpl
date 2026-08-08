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
{{- if and (gt (int .Values.replicaCount) 1) (not .Values.database.type) (not .Values.database.connectionSecret) -}}
{{- fail "replicaCount > 1 needs a shared database: with the on-disk default each replica keeps its own workflows, leases and users, so they cannot coordinate" -}}
{{- end -}}
{{- if and (gt (int .Values.replicaCount) 1) .Values.persistence.enabled (has "ReadWriteOnce" .Values.persistence.accessModes) -}}
{{- fail "replicaCount > 1 with a ReadWriteOnce volume will leave replicas after the first unschedulable; use ReadWriteMany or disable persistence and rely on the shared database" -}}
{{- end -}}
{{- end }}
