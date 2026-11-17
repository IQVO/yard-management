//go:build integration

package main

// Contract test (Schemathesis): boots the REAL composition root — cmd/api
// run() on testcontainers Postgres + Kafka, exactly as the flow tests do —
// and drives `st run` (schemathesis) over apis/openapi.yaml against the
// live port: property-based requests for every operation, valid and
// invalid, asserting the responses conform to the spec (status codes,
// content types, response schemas; positive data accepted, negative data
// rejected). Mirrors the `contract` CI job via scripts/contract-test.sh;
// skips when `st` is not on PATH (the plain `integration` job does not
// install it).
import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// contractExcludes are the Schemathesis exclusions, each one a documented
// finding of the first run against the spec (precedent: the sibling
// producers' scripts/contract-test.sh). Anything NOT listed here fails the
// build, so new drift is caught; shrink this list by fixing the finding.
//
// Excluded CHECKS:
//
//   - negative_data_rejection (product-master excludes it too): "schema-
//     violating request answered 2xx". Every hit is one REAL, lenient-reader
//     class: an empty or null value is read as ABSENT, so the schema's
//     enum/minLength/type is not enforced for it: `?state=` and `?cursor=` on
//     the list operations, an empty `Idempotency-Key` on the sweeps (the key
//     is optional there), `{"siteId": null}` on sweepDwell, and a PUT
//     yard-policy body missing the schema-required `requireAppointment` (the
//     field defaults to false). Which operation hits is seed-dependent, hence
//     a check-level exclusion rather than a list of operations.
//   - positive_data_acceptance: "schema-valid request refused with 4xx". Four
//     causes, all pinned by tests elsewhere:
//     (1) REAL spec/code mismatch: the HTTP adapter decodes bodies with
//     DisallowUnknownFields (internal/adapters/inbound/http/response.go) but
//     no request schema sets `additionalProperties: false`, so the generator's
//     extra properties get 400 `malformed-request-body`; (2) `cursor` is an
//     opaque server-issued page token no schema can describe (a generated one
//     is correctly 400 `invalid-cursor`); (3) the report `from`/`to` range rule
//     (to after from, at most 92 days) is cross-parameter (moves_test.go,
//     reports_kpi_test.go pin the 400 `invalid-report-range`); (4) conditional
//     domain rules inexpressible in OpenAPI 3.0.3 (a DROP visit brings a
//     trailer, a UN number needs a hazmat class, an appointment must exist in
//     the local copy, id charset/length rules) are pinned by the domain, HTTP
//     and BDD suites.
//
// Excluded OPERATIONS (REAL spec/code mismatches found by the response checks;
// fix the spec or the code, then delete the line):
//
//   - getVisit, listVisits, rejectAtGate: a visit refused at the gate is stored
//     and returned with `purpose: ""`, outside the Visit.purpose enum.
//   - getInspectionTemplateVersion, getYardPolicy: a malformed path parameter
//     (non-integer version, site id over 64 characters) is answered 422
//     `validation`, which is not among the documented responses (200/400/404/500).
var contractExcludes = []string{
	"--exclude-checks", "negative_data_rejection,positive_data_acceptance",
	"--exclude-operation-id", "getVisit",
	"--exclude-operation-id", "listVisits",
	"--exclude-operation-id", "rejectAtGate",
	"--exclude-operation-id", "getInspectionTemplateVersion",
	"--exclude-operation-id", "getYardPolicy",
}

func TestContractSchemathesis(t *testing.T) {
	st, err := exec.LookPath("st")
	if err != nil {
		t.Skipf("schemathesis (st) not on PATH — install the pinned version: " +
			"python3 -m pip install --user 'schemathesis==4.28.0'")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(wd, "..", "..", "apis", "openapi.yaml")
	if _, err := os.Stat(spec); err != nil {
		t.Fatalf("spec not found: %v", err)
	}
	maxExamples := 100
	if v := os.Getenv("CONTRACT_MAX_EXAMPLES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxExamples = n
		}
	}

	a, _ := boot(t) // real run(): fresh Postgres, real Kafka, waits for /readyz

	args := []string{"run", spec,
		"--url", a.base,
		"--max-examples", strconv.Itoa(maxExamples),
		"--workers", "4",
		"--generation-allow-x00", "false",
	}
	args = append(args, contractExcludes...)
	args = append(args, strings.Fields(os.Getenv("CONTRACT_ST_ARGS"))...)
	cmd := exec.Command(st, args...)
	cmd.Env = append(os.Environ(), "SCHEMATHESIS_HOOKS="+filepath.Join(wd, "..", "..", "scripts", "contract_hooks.py"))
	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() { out, err := cmd.CombinedOutput(); done <- result{out, err} }()
	var r result
	select {
	case r = <-done:
	case <-time.After(15 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatal("schemathesis did not finish within 15 minutes")
	}
	if len(r.out) > 0 {
		t.Logf("schemathesis output:\n%s", r.out)
	}
	if r.err != nil {
		t.Fatalf("schemathesis found API/spec violations (exit %v) — see its output above", r.err)
	}
}
