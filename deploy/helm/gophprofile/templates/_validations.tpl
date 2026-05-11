{{/*
gophprofile.validate — fail-fast guards for ambiguous values.yaml combinations
*/}}
{{- define "gophprofile.validate" -}}
  {{- if and .Values.secret.create .Values.secret.existingSecret -}}
    {{- fail "values.yaml conflict: set EITHER secret.create=true (chart manages the Secret) OR secret.existingSecret=<name> (you manage it externally), not both." -}}
  {{- end -}}
  {{- if and (not .Values.secret.create) (not .Values.secret.existingSecret) -}}
    {{- fail "values.yaml: set secret.create=true to let the chart render a Secret OR secret.existingSecret=<name> to reference one you manage." -}}
  {{- end -}}
{{- end -}}
