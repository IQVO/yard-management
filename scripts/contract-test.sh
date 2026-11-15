#!/usr/bin/env bash
# Contract-test the REST API with Schemathesis (property-based testing
# against apis/openapi.yaml): the integration-tagged Go test boots the
# REAL composition root (cmd/api run()) on testcontainers Postgres +
# Kafka on a loopback port, waits for /readyz, and drives `st run` over
# every operation in the spec — generating valid AND invalid requests
# and asserting the responses conform (status codes, content types,
# response schemas; positive data accepted, negative data rejected).
#
# The Schemathesis flags live next to the harness, in cmd/api/contract_integration_test.go
# (contractExcludes): every exclusion there is a documented finding of the
# first run — conditional rules OpenAPI 3.0.3 cannot express AND real spec/code
# mismatches — so anything new fails the build. CONTRACT_MAX_EXAMPLES (default
# 100) and CONTRACT_ST_ARGS (extra `st run` flags, e.g. --include-tag Visits)
# tune a local run. Needs Docker (testcontainers) and ~5-10 minutes.
#
# Mirrors the `contract` job in .github/workflows/ci.yml — same pinned
# Schemathesis version, same flags — so a local pass means a CI pass.
#
# Requires `st` on PATH:
#   python3 -m pip install --user 'schemathesis==4.28.0'
# (the exact version of .github/requirements/schemathesis.txt)
set -euo pipefail

SCHEMATHESIS_VERSION="4.28.0"

if ! command -v st >/dev/null 2>&1; then
  echo "schemathesis (st) is not installed (or not on PATH)."
  echo "Install the exact version CI pins (.github/requirements/schemathesis.txt):"
  echo "  python3 -m pip install --user 'schemathesis==${SCHEMATHESIS_VERSION}'"
  exit 1
fi

cd "$(dirname "$0")/.."
go test -tags=integration ./cmd/api -run '^TestContractSchemathesis$' -v -count=1 -timeout 25m
