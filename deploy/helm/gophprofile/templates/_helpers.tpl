{{/* Chart name + release-derived prefixes */}}
{{- define "gophprofile.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "gophprofile.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "gophprofile.server.name" -}}{{ include "gophprofile.fullname" . }}-server{{- end -}}
{{- define "gophprofile.worker.name" -}}{{ include "gophprofile.fullname" . }}-worker{{- end -}}

{{- define "gophprofile.labels" -}}
app.kubernetes.io/name: {{ include "gophprofile.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{- define "gophprofile.server.selectorLabels" -}}
app: gophprofile-server
app.kubernetes.io/name: {{ include "gophprofile.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: server
{{- end -}}

{{- define "gophprofile.worker.selectorLabels" -}}
app: gophprofile-worker
app.kubernetes.io/name: {{ include "gophprofile.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: worker
{{- end -}}

{{/* Image refs */}}
{{- define "gophprofile.serverImage" -}}
{{ .Values.image.registry }}/{{ .Values.image.serverRepository }}:{{ .Values.image.tag }}
{{- end -}}

{{- define "gophprofile.workerImage" -}}
{{ .Values.image.registry }}/{{ .Values.image.workerRepository }}:{{ .Values.image.tag }}
{{- end -}}

{{- define "gophprofile.migrateImage" -}}
{{ .Values.image.registry }}/{{ .Values.migrate.image.repository }}:{{ default .Values.image.tag .Values.migrate.image.tag }}
{{- end -}}

{{- define "gophprofile.secretName" -}}
{{- if .Values.secret.existingSecret -}}
{{ .Values.secret.existingSecret }}
{{- else -}}
{{ include "gophprofile.fullname" . }}-secrets
{{- end -}}
{{- end -}}
