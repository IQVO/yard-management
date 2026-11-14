#!/usr/bin/env bash
# Chart render assertions: Postgres and Kafka are the only runtime, so the
# mandatory configuration has no silent default, no in-memory / permissive mode
# is ever rendered, and the env the binaries read is the env the chart renders
# (chart-vs-binary check). Run: bash charts/yard-management/tests/render_test.sh
# (needs helm).
set -euxo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=yard-management
# Dummy values, no password: the chart refuses to render without a database
# source, Kafka brokers and the three consumer groups. None is ever dialled.
groups=(dockSchedulingConsumerGroup:yard-management-dock-scheduling
  facilityConsumerGroup:yard-management-facility
  inboundReceivingConsumerGroup:yard-management-inbound-receiving)
base=(--set database.url=postgres://u@example.invalid:5432/db
  --set kafka.brokers=kafka.example.invalid:9092)
for g in "${groups[@]}"; do base+=(--set "config.${g%%:*}=${g#*:}"); done
# Every optional value set (ct lint's values file), so every env var a binary
# can read is rendered.
everything=(-f "$chart_dir/ci/all-components-values.yaml")

fail() { echo "FAIL: $*" >&2; exit 1; }

configmap="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/configmap.yaml)"
api_deployment="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/deployment.yaml)"
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"

# Every config.* value is rendered into the chart ConfigMap, and the api
# Deployment -- the only workload whose binary (cmd/api) reads those keys --
# must mount it with envFrom, or a value that is only in the ConfigMap silently
# does nothing.
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' \
  || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" \
  || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"

# The other components read their own explicit env and must NOT inherit the
# api's ConfigMap (it would carry the api's consumer groups into them).
for tpl in mcp-deployment relay-deployment projector-deployment reports-deployment sweeps-cronjob; do
  if helm template "$release" "$chart_dir" "${everything[@]}" --show-only "templates/$tpl.yaml" | grep -q 'configMapRef:'; then
    fail "$tpl.yaml must not mount the api ConfigMap"
  fi
done

# Chart vs binary: every env var a binary reads must be rendered into its
# Deployment when every optional value is set. The names come from the binary's
# own os.Getenv / getenv("...") literals and its `env... = "NAME"` constants
# (cmd/*/config.go and main.go), so a new variable in a cmd/ source fails this
# test until the chart renders it.
scan_files() {
  ls "$repo_dir"/cmd/"$1"/*.go | grep -v -E '_test\.go$'
}
binary_env() {
  # getenv("NAME") / Getenv("NAME") literals plus `envFoo = "NAME"` constants.
  { grep -ohE '(Getenv|getenv)\("[A-Z][A-Z0-9_]*"' $(scan_files "$1") | sed -E 's/.*\("//; s/"$//'
    grep -ohE '^[[:space:]]*env[A-Za-z]+[[:space:]]*=[[:space:]]*"[A-Z][A-Z0-9_]*"' $(scan_files "$1") | sed -E 's/.*"([A-Z0-9_]+)"$/\1/'
  } | sort -u || true
}
assert_env_rendered() {
  local template="$1"; shift
  local rendered
  rendered="$(helm template "$release" "$chart_dir" "${everything[@]}" --show-only "templates/$template")"
  for var in "$@"; do
    grep -q "name: $var\$" <<<"$rendered" || fail "$template does not render $var, which the binary reads"
  done
}
# Word splitting is safe: the names are [A-Z0-9_] only (bash 3.2 has no mapfile).
# shellcheck disable=SC2207
api_vars=($(binary_env api))
# shellcheck disable=SC2207
mcp_vars=($(binary_env mcp))
# shellcheck disable=SC2207
relay_vars=($(binary_env relay))
# shellcheck disable=SC2207
projector_vars=($(binary_env yard-projector))
# shellcheck disable=SC2207
reports_vars=($(binary_env yard-reports))
(( ${#api_vars[@]} >= 8 )) || fail "found only ${#api_vars[@]} env names in cmd/api; the scan is looking in the wrong place"
(( ${#mcp_vars[@]} >= 4 )) || fail "found only ${#mcp_vars[@]} env names in cmd/mcp; the scan is looking in the wrong place"
(( ${#relay_vars[@]} >= 4 )) || fail "found only ${#relay_vars[@]} env names in cmd/relay; the scan is looking in the wrong place"
(( ${#projector_vars[@]} >= 4 )) || fail "found only ${#projector_vars[@]} env names in cmd/yard-projector; the scan is looking in the wrong place"
(( ${#reports_vars[@]} >= 4 )) || fail "found only ${#reports_vars[@]} env names in cmd/yard-reports; the scan is looking in the wrong place"
# MIGRATIONS_DATABASE_URL is read by the shared Postgres adapter
# (postgres.MigrationsURL) in api and mcp, not by a cmd/ literal; the relay's
# DATABASE_URL by postgres.EnvDatabaseURL; ANALYTICS_CONSUMER_GROUP by the Kafka
# adapter's constant (projector).
# ENVIRONMENT (deployment.environment.name) is read by telemetry.Setup inside
# internal/adapters/outbound/telemetry, not by a cmd/ literal: it is listed here.
# OTEL_SERVICE_NAME, OTEL_EXPORTER_OTLP_ENDPOINT and SERVICE_VERSION are read in
# cmd/*/telemetry.go, so the scan above already requires them.
assert_env_rendered mcp-deployment.yaml "${mcp_vars[@]}" MIGRATIONS_DATABASE_URL ENVIRONMENT
assert_env_rendered relay-deployment.yaml "${relay_vars[@]}" DATABASE_URL ENVIRONMENT
assert_env_rendered projector-deployment.yaml "${projector_vars[@]}" ANALYTICS_CONSUMER_GROUP ENVIRONMENT
assert_env_rendered reports-deployment.yaml "${reports_vars[@]}" ENVIRONMENT
for var in OTEL_SERVICE_NAME OTEL_EXPORTER_OTLP_ENDPOINT SERVICE_VERSION; do
  for vars_name in api_vars mcp_vars relay_vars projector_vars reports_vars; do
    eval "scanned=\" \${$vars_name[*]} \""
    [[ "$scanned" == *" $var "* ]] || fail "the env scan of $vars_name does not see $var: cmd/*/telemetry.go reads it with getenv(\"$var\")"
  done
done

# The documented env table (docs/docs/operations/configuration.md) lists exactly
# the variables each binary reads: the scanned names plus the ones the scan cannot
# see (read through a constant or by a shared adapter). A new variable fails here
# until the table names it, and a documented one that nothing reads fails too.
config_doc="$repo_dir/docs/docs/operations/configuration.md"
doc_vars() { # doc_vars <binary>: the backticked variable of each row of its section
  awk -v sec="## \`cmd/$1\`" '$0 == sec {on=1; next} /^## / {on=0} on && /^\| `[A-Z][A-Z0-9_]*` \|/ {sub(/^\| `/, ""); sub(/`.*/, ""); print}' "$config_doc" | sort -u
}
assert_documented() { # assert_documented <binary> <scanned...> -- <extra...>
  local bin="$1"; shift
  local want got
  want="$(printf '%s\n' "$@" | sort -u)"
  got="$(doc_vars "$bin")"
  [[ "$want" == "$got" ]] || fail "docs/docs/operations/configuration.md section cmd/$bin lists
$got
but the binary reads
$want"
}
assert_documented api "${api_vars[@]}" MIGRATIONS_DATABASE_URL ENVIRONMENT
assert_documented mcp "${mcp_vars[@]}" MIGRATIONS_DATABASE_URL ENVIRONMENT
assert_documented relay "${relay_vars[@]}" DATABASE_URL ENVIRONMENT
assert_documented yard-projector "${projector_vars[@]}" ANALYTICS_CONSUMER_GROUP ENVIRONMENT
assert_documented yard-reports "${reports_vars[@]}" ENVIRONMENT

# Every key the ConfigMap renders is a variable cmd/api reads (no dead keys).
full_configmap="$(helm template "$release" "$chart_dir" "${everything[@]}" --show-only templates/configmap.yaml)"
api_known=" ${api_vars[*]} DOCK_SCHEDULING_CONSUMER_GROUP FACILITY_CONSUMER_GROUP INBOUND_RECEIVING_CONSUMER_GROUP "
while read -r key; do
  [[ "$api_known" == *" $key "* ]] || fail "ConfigMap key $key is read by no binary"
done < <(awk '/^data:/ {d=1; next} d && /^  [A-Z]/ {sub(/:.*/, ""); sub(/^  /, ""); print}' <<<"$full_configmap")

# api env contract: every scanned variable is rendered either as an explicit env
# entry on the api Deployment or as a ConfigMap key (envFrom). The consumer
# groups are named through constants of the Kafka adapter, hence listed here.
api_everything="$(helm template "$release" "$chart_dir" "${everything[@]}" --show-only templates/deployment.yaml)"
for var in "${api_vars[@]}" MIGRATIONS_DATABASE_URL DOCK_SCHEDULING_CONSUMER_GROUP FACILITY_CONSUMER_GROUP INBOUND_RECEIVING_CONSUMER_GROUP; do
  grep -q "name: $var\$" <<<"$api_everything" || grep -q "^  $var:" <<<"$full_configmap" \
    || fail "the api reads $var but neither its Deployment nor its ConfigMap renders it"
done
# Every ConfigMap key is also an explicit api env entry carrying the same value
# source (the explicit entry wins over envFrom).
for var in HTTP_ADDR LOG_LEVEL SHUTDOWN_DRAIN_DELAY KAFKA_BROKERS DOCK_SCHEDULING_CONSUMER_GROUP FACILITY_CONSUMER_GROUP INBOUND_RECEIVING_CONSUMER_GROUP; do
  grep -q "^  $var:" <<<"$full_configmap" || fail "the ConfigMap does not carry $var"
  grep -q "name: $var\$" <<<"$api_everything" || fail "the api Deployment does not render $var"
done

# --- the mandatory configuration is rendered from values ------------------------------
env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap does not carry KAFKA_BROKERS"
for kv in 'DOCK_SCHEDULING_CONSUMER_GROUP=yard-management-dock-scheduling' 'FACILITY_CONSUMER_GROUP=yard-management-facility' \
  'INBOUND_RECEIVING_CONSUMER_GROUP=yard-management-inbound-receiving'; do
  [[ "$(env_value "$api_deployment" "${kv%%=*}")" == "${kv#*=}" ]] || fail "the api Deployment does not render $kv"
  grep -q "^  ${kv%%=*}: \"${kv#*=}\"" <<<"$configmap" || fail "the ConfigMap does not carry $kv"
done
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
# MIGRATIONS_DATABASE_URL: a Secret key, optional, and removable.
grep -A6 'name: MIGRATIONS_DATABASE_URL$' <<<"$api_deployment" | grep -q 'optional: true' || fail "MIGRATIONS_DATABASE_URL must be an optional secret key"
no_migrations="$(helm template "$release" "$chart_dir" "${base[@]}" --set database.migrationsExistingSecretKey= --show-only templates/deployment.yaml)"
if grep -q 'name: MIGRATIONS_DATABASE_URL' <<<"$no_migrations"; then fail "an empty database.migrationsExistingSecretKey must not render MIGRATIONS_DATABASE_URL"; fi
if grep -qE '^  (MIGRATIONS_|ANALYTICS_(READER_)?)?DATABASE_URL:' <<<"$full_configmap"; then fail "a DSN must never land in the ConfigMap"; fi
# Optional values render only when set.
for var in SHUTDOWN_DRAIN_DELAY; do
  if grep -q "name: $var" <<<"$api_deployment"; then fail "$var must not render while its value is empty"; fi
  if grep -q "^  $var:" <<<"$configmap"; then fail "$var must not land in the ConfigMap while its value is empty"; fi
done
if grep -q 'name: ANALYTICS_READER_DATABASE_URL' <<<"$api_deployment"; then fail "ANALYTICS_READER_DATABASE_URL must not render while analytics is off"; fi
render_all="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}")"
if grep -qiE 'EVENT_PUBLISHER|_MODE|permissive|in-memory' <<<"$render_all"$'\n'"$(cat "$chart_dir/templates/NOTES.txt")"; then
  fail "the render or NOTES.txt carries a publisher / mode switch or an in-memory mention; yard-management has none"
fi

# --- relay -----------------------------------------------------------------------------
relay_dep="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/relay-deployment.yaml)"
grep -q 'command: \["/app/relay"\]' <<<"$relay_dep" || fail "the relay Deployment must run /app/relay"
[[ "$(env_value "$relay_dep" OUTBOX_MAX_ATTEMPTS)" == "20" ]] || fail "OUTBOX_MAX_ATTEMPTS must default to 20 on the relay"
[[ "$(env_value "$relay_dep" RELAY_HEALTH_ADDR)" == ":8081" ]] || fail "RELAY_HEALTH_ADDR must default to :8081"
if grep -q 'name: OUTBOX_POLL_INTERVAL' <<<"$relay_dep"; then fail "OUTBOX_POLL_INTERVAL must not render while empty"; fi
tuned="$(helm template "$release" "$chart_dir" "${base[@]}" --set relay.outboxMaxAttempts=0 --set relay.outboxPollInterval=7s --show-only templates/relay-deployment.yaml)"
[[ "$(env_value "$tuned" OUTBOX_MAX_ATTEMPTS)" == "0" ]] || fail "relay.outboxMaxAttempts=0 (unlimited) is not rendered"
[[ "$(env_value "$tuned" OUTBOX_POLL_INTERVAL)" == "7s" ]] || fail "relay.outboxPollInterval is not rendered"
grep -A4 'name: DATABASE_URL$' <<<"$relay_dep" | grep -q 'secretKeyRef:' || fail "the relay DATABASE_URL must come from a Secret"
if helm template "$release" "$chart_dir" "${base[@]}" --set relay.enabled=false | grep -q 'app.kubernetes.io/component: relay'; then
  fail "relay.enabled=false must not render the relay"
fi

# --- mcp -------------------------------------------------------------------------------
mcp_dep="$(helm template "$release" "$chart_dir" "${base[@]}" --set mcp.enabled=true --show-only templates/mcp-deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp DATABASE_URL must come from a Secret"
[[ "$(env_value "$mcp_dep" MCP_ADDR)" == ":8090" ]] || fail "the mcp server must listen on :8090"
grep -q 'command: \["/app/mcp"\]' <<<"$mcp_dep" || fail "the mcp Deployment must run /app/mcp"
if helm template "$release" "$chart_dir" "${base[@]}" | grep -q 'app.kubernetes.io/component: mcp'; then fail "an mcp resource rendered with default values"; fi

# --- sweeps ----------------------------------------------------------------------------
sweeps="$(helm template "$release" "$chart_dir" "${base[@]}" --show-only templates/sweeps-cronjob.yaml)"
grep -q 'value: "http://yard-management:80/dwell-sweeps"' <<<"$sweeps" || fail "the dwell CronJob must POST to /dwell-sweeps on the api Service"
grep -q 'value: "http://yard-management:80/move-lease-sweeps"' <<<"$sweeps" || fail "the lease CronJob must POST to /move-lease-sweeps on the api Service"
grep -q 'schedule: "\*/5 \* \* \* \*"' <<<"$sweeps" || fail "the dwell sweep must default to every 5 minutes"
grep -q 'schedule: "\* \* \* \* \*"' <<<"$sweeps" || fail "the lease sweep must default to every minute"
grep -q 'concurrencyPolicy: Forbid' <<<"$sweeps" || fail "sweeps must not overlap"
if grep -q 'Idempotency-Key' <<<"$sweeps"; then fail "the sweeps are naturally idempotent and must not send an Idempotency-Key"; fi
if helm template "$release" "$chart_dir" "${base[@]}" --set sweeps.enabled=false | grep -q 'kind: CronJob'; then fail "sweeps.enabled=false must not render a CronJob"; fi
only_dwell="$(helm template "$release" "$chart_dir" "${base[@]}" --set sweeps.moveLeases.enabled=false)"
grep -q 'move-lease-sweeps' <<<"$only_dwell" && fail "sweeps.moveLeases.enabled=false must not render the lease CronJob"
grep -q 'dwell-sweeps' <<<"$only_dwell" || fail "the dwell CronJob disappeared with sweeps.moveLeases.enabled=false"

# --- refusals: the mandatory values name themselves -----------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
# args that satisfy everything except the one value a case removes.
mandatory_except() { # mandatory_except <database|kafka|<group key>>
  local skip="$1" out=()
  [[ "$skip" == database ]] || out+=(--set database.url=postgres://u@h/db)
  [[ "$skip" == kafka ]] || out+=(--set kafka.brokers=k:9092)
  for g in "${groups[@]}"; do
    [[ "${g%%:*}" == "$skip" ]] || out+=(--set "config.${g%%:*}=${g#*:}")
  done
  printf '%s\n' "${out[@]}"
}
refusal_without() { # refusal_without <value to leave out> <needle>
  local args=()
  while IFS= read -r line; do args+=("$line"); done < <(mandatory_except "$1")
  expect_refusal "no $1" "$2" "${args[@]}"
}
refusal_without database "database.url or database.existingSecret"
refusal_without kafka "kafka.brokers"
for g in "${groups[@]}"; do refusal_without "${g%%:*}" "config.${g%%:*}"; done
expect_refusal "kafka switched off" "Kafka cannot be switched off" "${base[@]}" --set kafka.enabled=false
expect_refusal "EVENT_PUBLISHER through extraEnv" "EVENT_PUBLISHER" "${base[@]}" --set 'extraEnv[0].name=EVENT_PUBLISHER' --set 'extraEnv[0].value=log'
expect_refusal "a *_MODE through extraEnv" "_MODE" "${base[@]}" --set 'extraEnv[0].name=DEMAND_MODE' --set 'extraEnv[0].value=off'
expect_refusal "a *_MODE through mcp.extraEnv" "_MODE" "${base[@]}" --set mcp.enabled=true --set 'mcp.extraEnv[0].name=LAYOUT_MODE' --set 'mcp.extraEnv[0].value=off'
expect_refusal "a *_MODE through relay.extraEnv" "_MODE" "${base[@]}" --set 'relay.extraEnv[0].name=LAYOUT_MODE' --set 'relay.extraEnv[0].value=off'
expect_refusal "a grace period under 20s" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=19
expect_refusal "no grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=0
helm template "$release" "$chart_dir" "${base[@]}" --set terminationGracePeriodSeconds=20 >/dev/null \
  || fail "a 20s grace period must still render"
expect_refusal "mcp without a database" "database.url or database.existingSecret" --set mcp.enabled=true --set kafka.brokers=k:9092 --set relay.enabled=false
expect_refusal "analytics without a DSN source" "analytics.url" "${base[@]}" --set analytics.enabled=true
expect_refusal "a sweep retry budget over the Job deadline" "worst-case retry budget" "${base[@]}" --set sweeps.maxAttempts=10
# kafka.enabled=true (what the sibling charts' infra values set) keeps rendering.
helm template "$release" "$chart_dir" "${base[@]}" --set kafka.enabled=true >/dev/null \
  || fail "kafka.enabled=true must keep rendering"

# --- analytics read side (ADR 0006) ----------------------------------------------------
# Off by default (neither component renders); with analytics on, the projector
# Deployment renders WITHOUT a Service (admin port only) and the reports
# Deployment + its Service render; the projector never sees the api's OLTP env
# and gets the brokers.
analytics=(--set analytics.enabled=true --set analytics.url=postgres://u@example.invalid:5432/analytics)
projector_dep="$(helm template "$release" "$chart_dir" "${base[@]}" "${analytics[@]}" --show-only templates/projector-deployment.yaml)"
reports_dep="$(helm template "$release" "$chart_dir" "${base[@]}" "${analytics[@]}" --show-only templates/reports-deployment.yaml)"
default_render="$(helm template "$release" "$chart_dir" "${base[@]}")"
if grep -q 'app.kubernetes.io/component: analytics-projector' <<<"$default_render"; then
  fail "the projector must not render while analytics.enabled is false"
fi
if grep -q 'app.kubernetes.io/component: analytics-reports' <<<"$default_render"; then
  fail "the reports component must not render while analytics.enabled is false"
fi
grep -q 'command: \["/app/yard-projector"\]' <<<"$projector_dep" || fail "the projector Deployment must run /app/yard-projector"
if grep -q 'kind: Service' <<<"$projector_dep"; then
  fail "the projector must not render a Service (its admin port is probes-only)"
fi
if grep -Eq 'name: (DATABASE_URL|MIGRATIONS_DATABASE_URL|DOCK_SCHEDULING_CONSUMER_GROUP)$' <<<"$projector_dep"; then
  fail "the projector Deployment must not get the api's OLTP env (it never opens the OLTP database)"
fi
[[ "$(env_value "$projector_dep" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "the projector KAFKA_BROKERS is not rendered from kafka.brokers"
[[ "$(env_value "$projector_dep" ANALYTICS_CONSUMER_GROUP)" == "yard-management-analytics" ]] || fail "ANALYTICS_CONSUMER_GROUP must default to yard-management-analytics"
grep -q 'command: \["/app/yard-reports"\]' <<<"$reports_dep" || fail "the reports Deployment must run /app/yard-reports"
grep -q 'kind: Service' <<<"$reports_dep" || fail "the reports Service was not rendered with analytics.enabled=true"
if grep -q 'name: KAFKA_BROKERS' <<<"$reports_dep"; then
  fail "the reports Deployment must not get KAFKA_BROKERS (it never dials Kafka)"
fi
grep -A6 'name: ANALYTICS_READER_DATABASE_URL$' <<<"$reports_dep" | grep -q 'optional: true' || fail "the reports reader DSN must be an optional secret key"
# The chart Secret carries the DSNs; the reader key only when readerUrl is set.
secret_plain="$(helm template "$release" "$chart_dir" "${base[@]}" "${analytics[@]}" --show-only templates/analytics-secret.yaml)"
grep -q 'ANALYTICS_DATABASE_URL:' <<<"$secret_plain" || fail "the analytics Secret must carry ANALYTICS_DATABASE_URL"
if grep -q 'ANALYTICS_READER_DATABASE_URL:' <<<"$secret_plain"; then fail "the reader key must not be created while analytics.readerUrl is empty"; fi
secret_reader="$(helm template "$release" "$chart_dir" "${base[@]}" "${analytics[@]}" --set analytics.readerUrl=postgres://r@example.invalid:5432/analytics --show-only templates/analytics-secret.yaml)"
grep -q 'ANALYTICS_READER_DATABASE_URL:' <<<"$secret_reader" || fail "analytics.readerUrl must create ANALYTICS_READER_DATABASE_URL"
# The api reads the reader DSN too while analytics is on.
api_analytics="$(helm template "$release" "$chart_dir" "${base[@]}" "${analytics[@]}" --show-only templates/deployment.yaml)"
grep -q 'name: ANALYTICS_READER_DATABASE_URL' <<<"$api_analytics" || fail "the api must read ANALYTICS_READER_DATABASE_URL while analytics is on"

# --- observability (ADR 0016) ----------------------------------------------------------
# Every component that runs a binary renders ENVIRONMENT, SERVICE_VERSION,
# OTEL_EXPORTER_OTLP_ENDPOINT and OTEL_SERVICE_NAME; the sweeps CronJobs run
# busybox wget (no yard-management binary) and render none.
otel_endpoint="otel-collector.observability.svc.cluster.local:4317"
for entry in deployment.yaml:yard-management mcp-deployment.yaml:yard-management-mcp relay-deployment.yaml:yard-management-relay \
  projector-deployment.yaml:yard-management-projector reports-deployment.yaml:yard-management-reports; do
  rendered="$(helm template "$release" "$chart_dir" "${everything[@]}" --show-only "templates/${entry%%:*}")"
  [[ "$(env_value "$rendered" OTEL_SERVICE_NAME)" == "${entry#*:}" ]] || fail "${entry%%:*} must render OTEL_SERVICE_NAME=${entry#*:}"
  [[ "$(env_value "$rendered" OTEL_EXPORTER_OTLP_ENDPOINT)" == "$otel_endpoint" ]] || fail "${entry%%:*} must render OTEL_EXPORTER_OTLP_ENDPOINT from otel.endpoint"
  [[ "$(env_value "$rendered" ENVIRONMENT)" == "local" ]] || fail "${entry%%:*} must render ENVIRONMENT from environment"
  [[ "$(env_value "$rendered" SERVICE_VERSION)" == "local" ]] || fail "${entry%%:*} must render SERVICE_VERSION (image.tag while serviceVersion is empty)"
done
for var in ENVIRONMENT SERVICE_VERSION OTEL_SERVICE_NAME OTEL_EXPORTER_OTLP_ENDPOINT; do
  if helm template "$release" "$chart_dir" "${everything[@]}" --show-only templates/sweeps-cronjob.yaml | grep -q "name: $var\$"; then
    fail "the sweeps CronJobs run wget, not a binary: they must not render $var"
  fi
done
# otel.serviceName and otel.endpoint are the values behind the names; serviceVersion wins over the tag.
renamed="$(helm template "$release" "$chart_dir" "${base[@]}" --set otel.serviceName=ym --set otel.endpoint=collector:4317 --set environment=staging --set serviceVersion=1.2.3 --set mcp.enabled=true)"
for want in 'OTEL_SERVICE_NAME ym' 'OTEL_SERVICE_NAME ym-mcp' 'OTEL_SERVICE_NAME ym-relay' 'OTEL_EXPORTER_OTLP_ENDPOINT collector:4317' 'ENVIRONMENT staging' 'SERVICE_VERSION 1.2.3'; do
  grep -A1 "name: ${want%% *}\$" <<<"$renamed" | grep -q "value: \"${want#* }\"" || fail "the render does not carry ${want% *}=${want#* }"
done
# otel.enabled=false drops the two OTEL_* names everywhere; ENVIRONMENT and SERVICE_VERSION stay.
quiet="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}" --set otel.enabled=false)"
if grep -qE 'name: OTEL_(SERVICE_NAME|EXPORTER_OTLP_ENDPOINT)$' <<<"$quiet"; then fail "otel.enabled=false must not render OTEL_SERVICE_NAME or OTEL_EXPORTER_OTLP_ENDPOINT"; fi
grep -q 'name: SERVICE_VERSION$' <<<"$quiet" || fail "SERVICE_VERSION is rendered whether or not otel is enabled"
# With the tag unset the version falls back to the chart appVersion.
fallback="$(helm template "$release" "$chart_dir" "${base[@]}" --set image.tag= --show-only templates/deployment.yaml)"
[[ "$(env_value "$fallback" SERVICE_VERSION)" == "$(awk -F'"' '/^appVersion:/ {print $2}' "$chart_dir/Chart.yaml")" ]] \
  || fail "SERVICE_VERSION must fall back to the chart appVersion"
# The four names belong to the chart: no extraEnv list may redefine them.
for list in extraEnv mcp.extraEnv relay.extraEnv analytics.projector.extraEnv analytics.reports.extraEnv; do
  for name in ENVIRONMENT SERVICE_VERSION OTEL_EXPORTER_OTLP_ENDPOINT OTEL_SERVICE_NAME; do
    expect_refusal "$name through $list" "$name" "${base[@]}" "${everything[@]}" --set "$list[0].name=$name" --set "$list[0].value=x"
  done
done

# ALL optional components render TOGETHER (ct lint's values file).
together="$(helm template "$release" "$chart_dir" "${base[@]}" "${everything[@]}")"
for component in api relay mcp analytics-projector analytics-reports sweep-dwell sweep-move-leases; do
  grep -q "app.kubernetes.io/component: $component" <<<"$together" \
    || fail "every component must render together; $component is missing"
done

echo "PASS: yard-management chart render assertions"
