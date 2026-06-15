{{/*
Expand the name of the chart.
*/}}
{{- define "instancez.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "instancez.fullname" -}}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Chart label.
*/}}
{{- define "instancez.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "instancez.labels" -}}
helm.sh/chart: {{ include "instancez.chart" . }}
{{ include "instancez.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "instancez.selectorLabels" -}}
app.kubernetes.io/name: {{ include "instancez.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Postgres service name (used in databaseUrl construction).
*/}}
{{- define "instancez.postgresServiceName" -}}
{{- printf "%s-postgres" (include "instancez.fullname" .) }}
{{- end }}

{{/*
Image tag — falls back to Chart.AppVersion when tag is empty.
*/}}
{{- define "instancez.imageTag" -}}
{{- .Values.image.tag | default .Chart.AppVersion }}
{{- end }}
