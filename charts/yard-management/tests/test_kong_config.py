#!/usr/bin/env python3
"""Assert this chart's Kong configuration contract.

Fleet decision (2026-10-13): ALL Kong configuration -- routes AND plugins --
belongs to the application's Helm chart, not warehouse-infra Terraform. The
old Terraform-side null_resource + kubectl objects silently vanished on a
cluster rebuild (local-exec only runs when triggers change) while
chart-rendered objects are Helm/ArgoCD-reconciled forever.

This test is the sensor for that decision. It fails if:
  * any enabled HTTPRoute renders WITHOUT the konghq.com/plugins annotation,
  * the annotation names KongPlugins the chart does not actually render,
  * any auth/authorization plugin (key-auth, jwt, oidc, acl, basic-auth,
    oauth2, ...) is ever configured -- forbidden fleet-wide,
  * the MCP route does not rewrite its prefix to /mcp or the reports route to
    /reports,
  * the CORS plugin does not allow Idempotency-Key and If-Match or does not
    expose ETag (the floor client's REST contract),
  * defaults (no --set flags) render any Kong object at all -- standalone
    installs without Kong must be unaffected.

Run: python3 charts/yard-management/tests/test_kong_config.py
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART = Path(__file__).resolve().parents[1]

FORBIDDEN_PLUGINS = {
    "basic-auth", "key-auth", "jwt", "oauth2", "oidc", "acl",
    "hmac-auth", "ldap-auth", "mtls", "session", "request-termination",
}

# The chart refuses to render without a database source, the Kafka brokers and
# the three consumer groups (Postgres and Kafka are the only runtime). Dummy
# values, none is ever dialled.
REAL_RUNTIME = [
    "--set", "database.url=postgres://user:pw@localhost:5432/db",
    "--set", "kafka.brokers=kafka.example.invalid:9092",
    "--set", "config.dockSchedulingConsumerGroup=yard-management-dock-scheduling",
    "--set", "config.facilityConsumerGroup=yard-management-facility",
    "--set", "config.inboundReceivingConsumerGroup=yard-management-inbound-receiving",
]

ENABLE_KONG = REAL_RUNTIME + [
    "--set", "gatewayApi.enabled=true",
    "--set", "gatewayApi.parentRefs[0].name=warehouse-gateway",
    "--set", "gatewayApi.parentRefs[0].namespace=kong",
    "--set", "gatewayApi.hosts[0].path=/api/yard-management",
    "--set", "kongPlugins.cors.enabled=true",
    "--set", "kongPlugins.prometheus.enabled=true",
    "--set", "kongPlugins.opentelemetry.enabled=true",
    "--set", "mcp.enabled=true",
    "--set", "mcp.route.enabled=true",
    "--set", "mcp.route.parentRefs[0].name=warehouse-gateway",
    "--set", "mcp.route.parentRefs[0].namespace=kong",
    "--set", "analytics.enabled=true",
    "--set", "analytics.url=postgres://user:pw@localhost:5432/analytics",
    "--set", "analytics.reports.route.enabled=true",
    "--set", "analytics.reports.route.parentRefs[0].name=warehouse-gateway",
    "--set", "analytics.reports.route.parentRefs[0].namespace=kong",
]


def render(extra_args: list[str]) -> list[dict]:
    out = subprocess.run(
        ["helm", "template", "yard-management", str(CHART), *extra_args],
        capture_output=True, text=True, check=True,
    ).stdout
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert Kong config", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(out) if d]


def main() -> int:
    failures: list[str] = []

    # 1. Defaults render zero Kong objects.
    default_docs = render(REAL_RUNTIME)
    kong_docs = [d for d in default_docs if "Kong" in (d.get("kind") or "")]
    if kong_docs:
        failures.append(
            f"default values render Kong objects {sorted(d['metadata']['name'] for d in kong_docs)} "
            "-- standalone installs must be unaffected; keep plugin/route blocks default-off"
        )
    if any(d.get("kind") == "HTTPRoute" for d in default_docs):
        failures.append("default values render an HTTPRoute; routes must be default-off")

    # 2. Enabled render: every HTTPRoute carries the plugins annotation.
    docs = render(ENABLE_KONG)
    plugins = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "KongPlugin"}
    routes = [d for d in docs if d.get("kind") == "HTTPRoute"]
    if len(routes) != 3:
        failures.append(f"enabled render has {len(routes)} HTTPRoutes, expected 3 (api, mcp, reports)")
    if not plugins:
        failures.append("enabled render contains no KongPlugin at all")
    for r in routes:
        ann = (r.get("metadata", {}).get("annotations") or {}).get("konghq.com/plugins")
        if not ann:
            failures.append(f"HTTPRoute {r['metadata']['name']} has no konghq.com/plugins annotation")
            continue
        for name in ann.split(","):
            if name not in plugins:
                failures.append(
                    f"HTTPRoute {r['metadata']['name']} references plugin '{name}' "
                    "that the chart does not render"
                )

    # 3. No forbidden (auth/authorization) plugin, ever.
    for name, p in plugins.items():
        plugin_type = p.get("plugin", "")
        if plugin_type in FORBIDDEN_PLUGINS:
            failures.append(
                f"KongPlugin {name} is '{plugin_type}' -- authentication/authorization "
                "plugins are forbidden at the gateway by fleet-wide decision"
            )

    # 4. Rewrite targets: MCP -> /mcp, reports -> /reports.
    for r in routes:
        name = r["metadata"]["name"]
        for rule in r.get("spec", {}).get("rules", []):
            for f in rule.get("filters", []):
                if f.get("type") == "URLRewrite":
                    target = f["urlRewrite"]["path"].get("replacePrefixMatch")
                    if name.endswith("-mcp") and target != "/mcp":
                        failures.append(f"MCP route {name} rewrites to {target!r}, expected '/mcp'")
                    if name.endswith("-reports") and target != "/reports":
                        failures.append(f"reports route {name} rewrites to {target!r}, expected '/reports'")

    # 5. CORS: the REST contract's custom headers.
    cors = [p for p in plugins.values() if p.get("plugin") == "cors"]
    for p in cors:
        cfg = p.get("config", {})
        for header in ("Idempotency-Key", "If-Match"):
            if header not in cfg.get("headers", []):
                failures.append(f"cors plugin does not allow the {header} request header")
        for header in ("ETag", "Idempotency-Replayed"):
            if header not in cfg.get("exposed_headers", []):
                failures.append(f"cors plugin does not expose the {header} response header")
        if "*" in cfg.get("origins", []):
            failures.append("cors plugin allows origin '*'; endpoints are unauthenticated, use the exact origin")
    if not cors:
        failures.append("enabled render contains no cors KongPlugin")

    if failures:
        print("FAIL: yard-management chart Kong-config contract")
        for f in failures:
            print(f"  - {f}")
        return 1
    print(f"PASS: {len(routes)} HTTPRoutes annotated, {len(plugins)} KongPlugins, no auth plugin, defaults are a no-op")
    return 0


if __name__ == "__main__":
    sys.exit(main())
