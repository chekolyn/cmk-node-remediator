{{/* Common labels */}}
{{- define "cmk-node-remediation.labels" -}}
app.kubernetes.io/name: cmk-node-remediation
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{/* Fullname */}}
{{- define "cmk-node-remediation.fullname" -}}
{{ .Release.Name }}-cmk-node-remediation
{{- end -}}
