{{/*
Expand the name of the chart.
*/}}
{{- define "yard-management.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "yard-management.fullname" -}}
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

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "yard-management.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "yard-management.labels" -}}
helm.sh/chart: {{ include "yard-management.chart" . }}
{{ include "yard-management.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels. Shared by every pod this release creates, so every
Deployment and Service ALSO pins app.kubernetes.io/component (api, mcp, relay,
analytics-projector, analytics-reports, sweep-*): a Service selecting on these
two alone would select every component.
*/}}
{{- define "yard-management.selectorLabels" -}}
app.kubernetes.io/name: {{ include "yard-management.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "yard-management.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "yard-management.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL: the operator's own
(database.existingSecret) or the one this chart creates from database.url.
*/}}
{{- define "yard-management.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "yard-management.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Fully qualified names of the MCP server, the relay and the analytics
components. The reports Service carries the same name as its Deployment
(different kinds, one name), like the api component.
*/}}
{{- define "yard-management.mcpFullname" -}}
{{- include "yard-management.fullname" . }}-mcp
{{- end }}

{{- define "yard-management.relayFullname" -}}
{{- include "yard-management.fullname" . }}-relay
{{- end }}

{{- define "yard-management.analyticsProjectorFullname" -}}
{{- include "yard-management.fullname" . }}-projector
{{- end }}

{{- define "yard-management.analyticsReportsFullname" -}}
{{- include "yard-management.fullname" . }}-reports
{{- end }}

{{/*
Image reference shared by every Deployment and CronJob.
*/}}
{{- define "yard-management.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/*
service.version of every component: serviceVersion when set, else the image tag,
else the chart appVersion. Never a literal of its own.
*/}}
{{- define "yard-management.serviceVersion" -}}
{{- .Values.serviceVersion | default .Values.image.tag | default .Chart.AppVersion -}}
{{- end -}}

{{/*
The observability env of one component (ADR 0016, the fleet observability
standard): ENVIRONMENT (deployment.environment.name) and SERVICE_VERSION
always; OTEL_EXPORTER_OTLP_ENDPOINT and OTEL_SERVICE_NAME only while
otel.enabled (off = the binary keeps its defaults and exports to nothing
reachable, costing telemetry only). Call with
  (dict "root" . "suffix" "<component>")
OTEL_SERVICE_NAME is otel.serviceName for the api (empty suffix) and
<otel.serviceName>-<suffix> for the others, the same names the binaries default
to (mcp, relay, projector, reports).
*/}}
{{- define "yard-management.telemetryEnv" -}}
{{- $root := .root -}}
- name: ENVIRONMENT
  value: {{ $root.Values.environment | quote }}
- name: SERVICE_VERSION
  value: {{ include "yard-management.serviceVersion" $root | quote }}
{{- if $root.Values.otel.enabled }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ $root.Values.otel.endpoint | quote }}
- name: OTEL_SERVICE_NAME
  value: {{ ternary $root.Values.otel.serviceName (printf "%s-%s" $root.Values.otel.serviceName .suffix) (empty .suffix) | quote }}
{{- end }}
{{- end -}}

{{/*
The four observability variables belong to the chart (telemetryEnv above):
a second definition through extraEnv would set the same variable twice, with
an undefined winner.
*/}}
{{- define "yard-management.requireNoChartOwnedEnv" -}}
{{- $where := .where -}}
{{- range .list -}}
{{- $name := toString .name -}}
{{- if has $name (list "ENVIRONMENT" "SERVICE_VERSION" "OTEL_EXPORTER_OTLP_ENDPOINT" "OTEL_SERVICE_NAME") -}}
{{- fail (printf "%s sets %s — the chart owns it: use environment, serviceVersion, otel.endpoint or otel.serviceName instead." $where $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: Postgres is the only runtime. cmd/api, cmd/mcp and cmd/relay exit at
boot without DATABASE_URL; there is no in-memory mode. Fails the render naming
the values instead of crash-looping.
*/}}
{{- define "yard-management.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "yard-management requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY: Kafka is the only runtime. cmd/api, cmd/mcp and cmd/relay exit at
boot without KAFKA_BROKERS; the projector needs the brokers too. There is no
default broker, no log sink and no way to switch Kafka off. kafka.enabled is
kept only so values written for the sibling charts still render: it may be
true, never false.
*/}}
{{- define "yard-management.requireKafka" -}}
{{- $_ := required "yard-management requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it." .Values.kafka.brokers -}}
{{- if and (hasKey .Values.kafka "enabled") (or (not .Values.kafka.enabled) (eq (toString .Values.kafka.enabled) "false")) -}}
{{- fail "kafka.enabled is false — Kafka cannot be switched off: the service runs on real Kafka only. Drop the key or set it to true and set kafka.brokers." -}}
{{- end -}}
{{- end -}}

{{/*
The three consumer groups of cmd/api are mandatory and STABLE (cmd/api exits at
boot without DOCK_SCHEDULING_CONSUMER_GROUP, FACILITY_CONSUMER_GROUP and
INBOUND_RECEIVING_CONSUMER_GROUP). A stable id is what lets a rescheduled pod
resume from its committed offsets.
*/}}
{{- define "yard-management.requireConsumerGroups" -}}
{{- range $c := list (dict "key" "dockSchedulingConsumerGroup" "env" "DOCK_SCHEDULING_CONSUMER_GROUP") (dict "key" "facilityConsumerGroup" "env" "FACILITY_CONSUMER_GROUP") (dict "key" "inboundReceivingConsumerGroup" "env" "INBOUND_RECEIVING_CONSUMER_GROUP") -}}
{{- if not (index $.Values.config $c.key) -}}
{{- fail (printf "config.%s is required (%s): the consumer always runs on real Kafka and cmd/api exits at boot without a group. Use a STABLE id." $c.key $c.env) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not smuggle a different runtime in: the service has no
EVENT_PUBLISHER and no *_MODE variable at all (Postgres and Kafka are the only
runtime), so any such name is a mistake copied from another chart.
*/}}
{{- define "yard-management.requireNoInMemoryEnv" -}}
{{- $where := .where -}}
{{- range .list -}}
{{- $name := toString .name -}}
{{- if or (eq $name "EVENT_PUBLISHER") (hasSuffix "_MODE" $name) -}}
{{- fail (printf "%s sets %s — yard-management has no publisher or mode switch: it runs on real Postgres and real Kafka only." $where $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The api's graceful shutdown is sequential (SHUTDOWN_DRAIN_DELAY 5s + HTTP drain
+ consumers drain). A grace period under 20s would SIGKILL the pod between
"consumers stopped" and "pool closed", stranding an uncommitted offset.
*/}}
{{- define "yard-management.requireGracePeriod" -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) 20 -}}
{{- fail (printf "terminationGracePeriodSeconds is %v — want at least 20: the consumers drain after the HTTP server, and a shorter grace period SIGKILLs them mid-drain." .Values.terminationGracePeriodSeconds) -}}
{{- end -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install` whichever component is enabled.
*/}}
{{- define "yard-management.requireRealRuntime" -}}
{{- include "yard-management.requireDatabase" . -}}
{{- include "yard-management.requireKafka" . -}}
{{- include "yard-management.requireConsumerGroups" . -}}
{{- include "yard-management.requireNoInMemoryEnv" (dict "list" .Values.extraEnv "where" "extraEnv") -}}
{{- include "yard-management.requireNoInMemoryEnv" (dict "list" .Values.mcp.extraEnv "where" "mcp.extraEnv") -}}
{{- include "yard-management.requireNoInMemoryEnv" (dict "list" .Values.relay.extraEnv "where" "relay.extraEnv") -}}
{{- include "yard-management.requireNoChartOwnedEnv" (dict "list" .Values.extraEnv "where" "extraEnv") -}}
{{- include "yard-management.requireNoChartOwnedEnv" (dict "list" .Values.mcp.extraEnv "where" "mcp.extraEnv") -}}
{{- include "yard-management.requireNoChartOwnedEnv" (dict "list" .Values.relay.extraEnv "where" "relay.extraEnv") -}}
{{- include "yard-management.requireNoChartOwnedEnv" (dict "list" .Values.analytics.projector.extraEnv "where" "analytics.projector.extraEnv") -}}
{{- include "yard-management.requireNoChartOwnedEnv" (dict "list" .Values.analytics.reports.extraEnv "where" "analytics.reports.extraEnv") -}}
{{- include "yard-management.requireGracePeriod" . -}}
{{- end -}}

{{/*
Name of the Secret holding ANALYTICS_DATABASE_URL (and, when analytics.readerUrl
is set, ANALYTICS_READER_DATABASE_URL): the operator's own
(analytics.existingSecret) or the one this chart creates from analytics.url
(analytics-secret.yaml).
*/}}
{{- define "yard-management.analyticsSecretName" -}}
{{- if .Values.analytics.existingSecret -}}
{{- .Values.analytics.existingSecret -}}
{{- else -}}
{{- include "yard-management.fullname" . }}-analytics
{{- end -}}
{{- end }}

{{/*
Analytics guard (ADR 0006): the projector and reports binaries exit at boot
without ANALYTICS_DATABASE_URL. The projector consumes
warehouse.yard-management.analytics from kafka.brokers, which the api
Deployment already requires (requireKafka), so only the DSN source is checked
here, and only while analytics is on.
*/}}
{{- define "yard-management.requireAnalyticsConfig" -}}
{{- if not (or .Values.analytics.url .Values.analytics.existingSecret) -}}
{{- fail "analytics.enabled is true but neither analytics.url nor analytics.existingSecret is set — the projector and reports binaries exit at boot without ANALYTICS_DATABASE_URL." -}}
{{- end -}}
{{- end -}}

{{/*
Comma-joined names of this chart's enabled KongPlugins, for the
konghq.com/plugins annotation on every HTTPRoute this chart renders.
Empty string (annotation omitted) when none are enabled.
*/}}
{{- define "yard-management.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "yard-management.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}
