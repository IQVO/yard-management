"""Schemathesis hooks for scripts/contract-test.sh (the cmd/api contract test).

Idempotency-Key: every POST/PUT of this service sits behind the Idempotency-Key
middleware (1-128 printable ASCII characters; the same key with a different
body is 409 `idempotency-key-reused`). The OpenAPI document gives the parameter
only `minLength`/`maxLength`, so Schemathesis would send non-printable or
repeated keys. A real client sends a fresh UUID per logical submit; this hook
does the same for every request that carries the header. A request that
deliberately omits the header (negative testing of the required parameter) is
left alone and must still be answered 400, and so is a case whose HEADER
component was generated as NEGATIVE data (e.g. a 129-character key): replacing
it would turn a deliberate violation into a valid request and the server would
rightly accept it.
"""

import uuid

import schemathesis
from schemathesis.core.parameters import ParameterLocation
from schemathesis.generation import GenerationMode


@schemathesis.hook
def before_call(ctx, case, **kwargs):
    if not (case.headers and "Idempotency-Key" in case.headers):
        return None
    components = getattr(getattr(case, "meta", None), "components", None) or {}
    header = components.get(ParameterLocation.HEADER)
    if header is not None and header.mode == GenerationMode.NEGATIVE:
        return None
    case.headers["Idempotency-Key"] = str(uuid.uuid4())
    return None
