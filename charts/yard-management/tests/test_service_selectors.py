#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists (fleet-wide): `app.kubernetes.io/name` + `instance` are identical
on every pod a release creates (api, mcp, relay, projector, reports, sweeps). A
Service that selects on those two alone selects ALL of them -- verified live in
other fleet charts, where a Kong request for an api /healthz was answered by the
wrong pod. Every Deployment here carries `app.kubernetes.io/component` in
selector.matchLabels and its pod labels, and every Service selects on it, so each
Service selects EXACTLY ONE Deployment. This test fails if that ever stops being
true.

It mirrors warehouse-infra's scripts/check-chart-selectors.py, restricted to the
components this chart has: api (always), relay (default on, no Service), mcp
(optional, default off), the analytics read side of ADR 0006 -- analytics-projector
(no Service) and analytics-reports -- and the two sweep CronJobs (default on, no
Service), run with every other component enabled TOGETHER.

Run: python3 charts/yard-management/tests/test_service_selectors.py
Needs: helm, PyYAML.
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "yard-management"

# Dummy values, no password: the chart refuses to render without a database
# source, the Kafka brokers and the three consumer groups (Postgres and Kafka
# are the only runtime). None is ever dialled.
GROUPS = {
    "dockSchedulingConsumerGroup": "yard-management-dock-scheduling",
    "facilityConsumerGroup": "yard-management-facility",
    "inboundReceivingConsumerGroup": "yard-management-inbound-receiving",
}


def runtime(skip: str | None = None) -> list[str]:
    """The mandatory values, minus the one named by skip (database, kafka or a group key)."""
    args: list[str] = []
    if skip != "database":
        args += ["--set", "database.url=postgres://u@example.invalid:5432/db"]
    if skip != "kafka":
        args += ["--set", "kafka.brokers=kafka.example.invalid:9092"]
    for key, value in GROUPS.items():
        if skip != key:
            args += ["--set", f"config.{key}={value}"]
    return args


BASE = runtime()
ENABLE_EVERYTHING = BASE + [
    "--set", "mcp.enabled=true",
    "--set", "autoscaling.api.enabled=true",
    "--set", "config.shutdownDrainDelay=5s",
    "--set", "relay.outboxPollInterval=500ms",
    "--set", "kafka.enabled=true",
    "--set", "gatewayApi.enabled=true",
    "--set", "ingress.enabled=true",
    # ADR 0006: the analytics read side ON together with everything else.
    "--set", "analytics.enabled=true",
    "--set", "analytics.url=postgres://u@example.invalid:5432/analytics",
    "--set", "analytics.readerUrl=postgres://r@example.invalid:5432/analytics",
    "--set", "analytics.consumerGroup=yard-management-analytics",
]

# Every env var cmd/api reads, each with a dedicated chart value.
API_ENV = (
    "HTTP_ADDR", "LOG_LEVEL", "SHUTDOWN_DRAIN_DELAY", "KAFKA_BROKERS", "DATABASE_URL",
    "MIGRATIONS_DATABASE_URL", "ANALYTICS_READER_DATABASE_URL",
    "DOCK_SCHEDULING_CONSUMER_GROUP", "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP",
)


def helm_template(extra_args: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["helm", "template", RELEASE, str(CHART_DIR), *extra_args],
        capture_output=True, text=True,
    )


def render(extra_args: list[str]) -> list[dict]:
    result = helm_template(extra_args)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(extra_args)}:\n{result.stderr}")
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def env_of(dep: dict) -> list[dict]:
    return dep["spec"]["template"]["spec"]["containers"][0].get("env", [])


def env_names(dep: dict) -> list[str]:
    return [e["name"] for e in env_of(dep)]


def env_value(dep: dict, name: str) -> str | None:
    return next((e.get("value") for e in env_of(dep) if e["name"] == name), None)


def command_of(dep: dict) -> list[str] | None:
    return dep["spec"]["template"]["spec"]["containers"][0].get("command")


def check_mcp(services: dict, deployments: dict, failures: list[str]) -> None:
    mcp = f"{RELEASE}-mcp"
    if mcp not in services:
        failures.append("the MCP Service was not rendered with mcp.enabled=true")
    elif selector_of(services[mcp]).get("app.kubernetes.io/component") != "mcp":
        failures.append("the MCP Service selector must pin component=mcp")
    if mcp not in deployments:
        failures.append("the MCP Deployment was not rendered with mcp.enabled=true")
        return
    if command_of(deployments[mcp]) != ["/app/mcp"]:
        failures.append("the MCP Deployment must run /app/mcp")
    mcp_env = set(env_names(deployments[mcp]))
    # cmd/mcp starts no relay and no consumer: none of the api's consumer groups
    # or the relay's tuning may reach it.
    stray = {
        "OUTBOX_MAX_ATTEMPTS", "OUTBOX_POLL_INTERVAL", "RELAY_HEALTH_ADDR", "ANALYTICS_READER_DATABASE_URL",
        "DOCK_SCHEDULING_CONSUMER_GROUP", "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP",
    } & mcp_env
    if stray:
        failures.append(f"the MCP Deployment must not get relay/consumer env: {sorted(stray)}")
    for want in ("DATABASE_URL", "MIGRATIONS_DATABASE_URL", "KAFKA_BROKERS", "MCP_ADDR", "LOG_LEVEL", "SHUTDOWN_DRAIN_DELAY"):
        if want not in mcp_env:
            failures.append(f"the MCP Deployment does not render {want}")


def check_relay(services: dict, deployments: dict, failures: list[str]) -> None:
    relay = f"{RELEASE}-relay"
    if relay not in deployments:
        failures.append("the relay Deployment was not rendered (relay.enabled defaults to true)")
        return
    if relay in services:
        failures.append("the relay must not render a Service (its health port is probes-only)")
    if command_of(deployments[relay]) != ["/app/relay"]:
        failures.append("the relay Deployment must run /app/relay")
    relay_env = set(env_names(deployments[relay]))
    stray = {
        "MIGRATIONS_DATABASE_URL", "ANALYTICS_READER_DATABASE_URL", "DOCK_SCHEDULING_CONSUMER_GROUP",
        "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP", "SHUTDOWN_DRAIN_DELAY", "HTTP_ADDR",
    } & relay_env
    if stray:
        failures.append(f"the relay Deployment must not get api-only env: {sorted(stray)}")
    for want in ("DATABASE_URL", "KAFKA_BROKERS", "OUTBOX_MAX_ATTEMPTS", "OUTBOX_POLL_INTERVAL", "RELAY_HEALTH_ADDR"):
        if want not in relay_env:
            failures.append(f"the relay Deployment does not render {want}")


def check_analytics(services: dict, deployments: dict, failures: list[str]) -> None:
    projector, reports = f"{RELEASE}-projector", f"{RELEASE}-reports"
    if projector not in deployments:
        failures.append("the projector Deployment was not rendered with analytics.enabled=true")
    else:
        if command_of(deployments[projector]) != ["/app/yard-projector"]:
            failures.append("the projector Deployment must run /app/yard-projector")
        projector_env = set(env_names(deployments[projector]))
        # The projector never opens the OLTP database and runs none of the api's
        # consumers; only its own env may reach it.
        stray = {
            "DATABASE_URL", "MIGRATIONS_DATABASE_URL", "OUTBOX_MAX_ATTEMPTS", "OUTBOX_POLL_INTERVAL",
            "DOCK_SCHEDULING_CONSUMER_GROUP", "FACILITY_CONSUMER_GROUP", "INBOUND_RECEIVING_CONSUMER_GROUP",
            "ANALYTICS_READER_DATABASE_URL",
        } & projector_env
        if stray:
            failures.append(f"the projector Deployment must not get the api's OLTP env: {sorted(stray)}")
        for want in ("ANALYTICS_DATABASE_URL", "KAFKA_BROKERS", "ANALYTICS_CONSUMER_GROUP", "ADMIN_ADDR", "LOG_LEVEL"):
            if want not in projector_env:
                failures.append(f"the projector Deployment does not render {want}")
    if reports not in deployments:
        failures.append("the reports Deployment was not rendered with analytics.enabled=true")
    else:
        if command_of(deployments[reports]) != ["/app/yard-reports"]:
            failures.append("the reports Deployment must run /app/yard-reports")
        reports_env = set(env_names(deployments[reports]))
        # The reports binary never dials Kafka and never opens OLTP.
        stray = {"KAFKA_BROKERS", "DATABASE_URL", "MIGRATIONS_DATABASE_URL"} & reports_env
        if stray:
            failures.append(f"the reports Deployment must not get Kafka/OLTP env (it reads the analytical database only): {sorted(stray)}")
        for want in ("ANALYTICS_READER_DATABASE_URL", "ANALYTICS_DATABASE_URL", "HTTP_ADDR", "LOG_LEVEL", "SHUTDOWN_DRAIN_DELAY"):
            if want not in reports_env:
                failures.append(f"the reports Deployment does not render {want}")
    if reports not in services:
        failures.append("the reports Service was not rendered with analytics.enabled=true")
    elif selector_of(services[reports]).get("app.kubernetes.io/component") != "analytics-reports":
        failures.append("the reports Service selector must pin component=analytics-reports")
    if projector in services:
        failures.append("the projector must not render a Service (its admin port is probes-only)")


def check_sweeps(docs: list[dict], failures: list[str]) -> None:
    cronjobs = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "CronJob"}
    want = {
        f"{RELEASE}-sweep-dwell": "dwell-sweeps",
        f"{RELEASE}-sweep-move-leases": "move-lease-sweeps",
    }
    for name, path in want.items():
        job = cronjobs.get(name)
        if job is None:
            failures.append(f"the CronJob {name} was not rendered (sweeps.enabled defaults to true)")
            continue
        pod = job["spec"]["jobTemplate"]["spec"]["template"]
        container = pod["spec"]["containers"][0]
        env = {e["name"]: e.get("value") for e in container.get("env", [])}
        if env.get("SWEEP_URL") != f"http://{RELEASE}:80/{path}":
            failures.append(f"CronJob {name} posts to {env.get('SWEEP_URL')!r}, want http://{RELEASE}:80/{path}")
        if job["spec"].get("concurrencyPolicy") != "Forbid":
            failures.append(f"CronJob {name} must forbid overlapping runs")
        if pod["spec"].get("restartPolicy") != "Never":
            failures.append(f"CronJob {name} pods must not restart")
        if "app.kubernetes.io/component" not in (pod["metadata"].get("labels") or {}):
            failures.append(f"CronJob {name} pods carry no component label")


def check_components(docs: list[dict], failures: list[str]) -> None:
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if RELEASE not in services:
        failures.append("the api Service was not rendered")
    elif selector_of(services[RELEASE]).get("app.kubernetes.io/component") != "api":
        failures.append("the api Service selector must pin component=api")

    check_mcp(services, deployments, failures)
    check_relay(services, deployments, failures)
    check_analytics(services, deployments, failures)
    check_sweeps(docs, failures)

    # Every Deployment's own selector must pin a component too, and be
    # satisfied by its pod labels.
    for name, dep in deployments.items():
        match_labels = dep["spec"]["selector"].get("matchLabels") or {}
        if "app.kubernetes.io/component" not in match_labels:
            failures.append(f"Deployment {name} selector.matchLabels lacks app.kubernetes.io/component")
        if not matches(match_labels, pod_labels_of(dep)):
            failures.append(f"Deployment {name} pod labels do not satisfy its own selector")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1")
        # ...and no CronJob pod.
        for d in docs:
            if d.get("kind") == "CronJob":
                labels = d["spec"]["jobTemplate"]["spec"]["template"]["metadata"].get("labels") or {}
                if matches(sel, labels):
                    failures.append(f"Service {svc_name} also selects the pods of CronJob {d['metadata']['name']}")

    # The api HPA owns replicas when enabled.
    hpa = next((d for d in docs if d.get("kind") == "HorizontalPodAutoscaler"), None)
    if hpa is None or hpa["spec"]["scaleTargetRef"]["name"] != RELEASE:
        failures.append("the api HPA was not rendered (or does not scale the api Deployment)")
    elif "replicas" in deployments.get(RELEASE, {}).get("spec", {}):
        failures.append("the api Deployment must omit replicas when its HPA owns them")

    api = deployments.get(RELEASE)
    if api is None:
        failures.append("the api Deployment was not rendered")
        return
    api_env = env_names(api)
    for want in API_ENV:
        if want not in api_env:
            failures.append(f"the api Deployment does not render {want} from its dedicated value")
    duplicates = sorted({n for n in api_env if api_env.count(n) > 1})
    if duplicates:
        failures.append(f"the api Deployment renders env vars twice: {duplicates}")
    if command_of(api) != ["/app/api"]:
        failures.append("the api Deployment must run /app/api")
    for name, want in (("SHUTDOWN_DRAIN_DELAY", "5s"),):
        if env_value(api, name) != want:
            failures.append(f"the api Deployment renders {name}={env_value(api, name)!r}, want {want!r}")


def check_refusals(failures: list[str]) -> None:
    cases: list[tuple[str, list[str], str]] = [
        ("without a database source", runtime("database"), "requires database.url or database.existingSecret"),
        ("without kafka.brokers", runtime("kafka"), "kafka.brokers"),
        ("kafka switched off", BASE + ["--set", "kafka.enabled=false"], "Kafka cannot be switched off"),
        ("EVENT_PUBLISHER through extraEnv",
         BASE + ["--set", "extraEnv[0].name=EVENT_PUBLISHER", "--set", "extraEnv[0].value=kafka"], "extraEnv sets EVENT_PUBLISHER"),
        ("a *_MODE through extraEnv",
         BASE + ["--set", "extraEnv[0].name=DEMAND_MODE", "--set", "extraEnv[0].value=kafka"], "extraEnv sets DEMAND_MODE"),
        ("a *_MODE through relay.extraEnv",
         BASE + ["--set", "relay.extraEnv[0].name=DEMAND_MODE", "--set", "relay.extraEnv[0].value=kafka"], "relay.extraEnv sets DEMAND_MODE"),
        ("a grace period under 20s", BASE + ["--set", "terminationGracePeriodSeconds=19"], "terminationGracePeriodSeconds"),
        ("analytics without a DSN source", BASE + ["--set", "analytics.enabled=true"],
         "neither analytics.url nor analytics.existingSecret"),
        ("a sweep retry budget over the Job deadline", BASE + ["--set", "sweeps.maxAttempts=10"], "worst-case retry budget"),
        ("mcp without a database source",
         ["--set", "mcp.enabled=true", "--set", "kafka.brokers=k:9092", "--set", "relay.enabled=false"]
         + [x for k, v in GROUPS.items() for x in ("--set", f"config.{k}={v}")],
         "requires database.url or database.existingSecret"),
    ]
    for key in GROUPS:
        cases.append((f"without config.{key}", runtime(key), f"config.{key} is required"))
    for label, args, needle in cases:
        result = helm_template(args)
        if result.returncode == 0 or needle not in result.stderr:
            failures.append(f"chart rendered (or failed for another reason) {label}")


def main() -> int:
    failures: list[str] = []

    check_components(render(ENABLE_EVERYTHING), failures)

    # Default values must not deploy the MCP or analytics components, an HPA or
    # a route; the relay and the sweeps are on.
    defaults = render(BASE)
    stray = [d["metadata"]["name"] for d in defaults if d["metadata"]["name"].endswith(("-mcp", "-projector", "-reports", "-analytics"))]
    stray += [d["kind"] for d in defaults if d.get("kind") in {"HorizontalPodAutoscaler", "Ingress", "HTTPRoute"}]
    if stray:
        failures.append(f"optional components rendered with default values: {stray}")
    names_by_kind = {(d.get("kind"), d["metadata"]["name"]) for d in defaults}
    for want in (("Deployment", f"{RELEASE}-relay"), ("CronJob", f"{RELEASE}-sweep-dwell"), ("CronJob", f"{RELEASE}-sweep-move-leases")):
        if want not in names_by_kind:
            failures.append(f"{want[0]} {want[1]} must render with default values")
    dep = next(d for d in defaults if d.get("kind") == "Deployment" and d["metadata"]["name"] == RELEASE)
    names = env_names(dep)
    off = {"SHUTDOWN_DRAIN_DELAY", "ANALYTICS_READER_DATABASE_URL"}
    if off & set(names):
        failures.append(f"these must be off with default values: {sorted(off & set(names))}")
    for want in ("KAFKA_BROKERS", *(k for k in API_ENV if k.endswith("_CONSUMER_GROUP"))):
        if want not in names:
            failures.append(f"{want} is mandatory and must always be rendered")

    check_refusals(failures)

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print("PASS: every Service selects exactly one Deployment (api, mcp, analytics-reports) and no CronJob pod; relay and sweeps "
          "are on by default; mcp, analytics, HPA and routes are off by default; every cmd/api env var has a dedicated value; the "
          "chart refuses to render without a database source, brokers or any consumer group, with kafka switched off, with a "
          "mode or publisher switch through extraEnv, with a grace period under 20s, with a sweep retry budget over the Job "
          "deadline, or with analytics on but no DSN source")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
