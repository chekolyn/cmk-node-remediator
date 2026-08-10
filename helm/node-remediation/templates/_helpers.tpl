{{/* Common labels */}}
{{- define "node-remediation.labels" -}}
app.kubernetes.io/name: node-remediation
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

{{/* Fullname */}}
{{- define "node-remediation.fullname" -}}
{{ .Release.Name }}-node-remediation
{{- end -}}
