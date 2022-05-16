{{/*
Create serviceAccountName for a resource. Needs appName or serviceAccountName value in he scope!
*/}}
{{- define "data-collecting.serviceAccountName" -}}
{{- $top := index . 0 }}
{{- $context := index . 1 }}
{{- if $context.serviceAccountName }}
{{- $context.serviceAccountName }}
{{- else }}
{{- print $context.appName "-" $top.Release.Name "-sa" }}
{{- end }}
{{- end }}

{{/*
Transform container args from map to a list of the form:
"-key1"
"value1"
"-key2"
"value2"
etc.
Passed context is the map.
*/}}
{{- define "data-collecting.transformArgs" -}}
{{- range $key, $value := . }}
- {{ print "-" $key | quote }}
- {{ $value | quote }}
{{- end }}
{{- end }}

{{/*
Creates a name of the form <release>-<basename>-cr
Needs basename as the passed context
*/}}
{{- define "data-collecting.clusterRoleName" -}}
{{- $top := index . 0 }}
{{- $context := index . 1 }}
{{- print $top.Release.Name "-" $context "-cr" }}
{{- end }}

{{/*
Expand the name of the chart.
*/}}
{{- define "data-collecting.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "data-collecting.fullname" -}}
{{- $top := index . 0 }}
{{- $context := index . 1 }}
{{- if $context.appName }}
{{- $context.appName | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name $context.appName }}
{{- if contains $name $top.Release.Name }}
{{- $top.Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" $top.Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "data-collecting.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "data-collecting.labels" -}}
helm.sh/chart: {{ include "data-collecting.chart" . }}
{{ include "data-collecting.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "data-collecting.selectorLabels" -}}
app.kubernetes.io/name: {{ include "data-collecting.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
