#!/usr/bin/env python3
"""AsyncAPI 3.0 producer breaking-change gate (yard-management).

Compares this PR's apis/asyncapi.yaml against the base branch's copy and
fails the build on the changes that break the consumers that PIN this
spec (contracts/pinned.json pins apis/asyncapi.yaml@develop):

  dock-scheduling, exception-management, inbound-receiving, outbound-shipping

(source of truth: warehouse-docs network/model/contract-edges.json on
origin/main, read-only). Their contract tests only see a change after
they refresh the pin, so a breaking change must be caught HERE, in the
producer's PR.

Handles this spec's AsyncAPI 3.0 shape: channels keyed by logical name
with an `address`, channel-level `messages` maps, payloads that are
allOf(CloudEventEnvelope, {data: $ref <Name>Data}). Breaking:

  1. a channel is removed;
  2. a message is removed from a channel's messages map;
  3. a required data field is removed, renamed, or stops being required;
  4. a data field's type narrows (the type set loses an option — e.g.
     nullability dropped — or an enum appears/shrinks).

Everything else (a new message, a new optional field, description edits,
widening a type) is additive and passes. Per the envelope contract, a
breaking payload change ships as a new `.v2` dataschema/type, never as a
mutation of the pinned one.

Usage:
  python3 scripts/harness/asyncapi_breaking.py BASE.yaml HEAD.yaml
  python3 scripts/harness/asyncapi_breaking.py --self-check
"""
import os
import sys

import yaml

PINNING_CONSUMERS = ("dock-scheduling", "exception-management",
                     "inbound-receiving", "outbound-shipping")
CONSUMERS_NOTE = ("Pinning consumers (warehouse-docs network/model/"
                  "contract-edges.json, origin/main): "
                  + ", ".join(PINNING_CONSUMERS) + ".")


def deref(node, root):
    """Follow local #$ref chains to a concrete node."""
    hops = 0
    while isinstance(node, dict) and isinstance(node.get("$ref"), str) and node["$ref"].startswith("#/"):
        ptr = root
        for part in node["$ref"][2:].split("/"):
            ptr = ptr[part.replace("~1", "/").replace("~0", "~")]
        node = ptr
        hops += 1
        if hops > 50:
            raise ValueError("$ref loop at " + node.get("$ref", "?"))
    return node


def data_schema(message, root):
    """The dereferenced `data` schema of a message payload (allOf-aware).

    The payload is allOf(CloudEventEnvelope, <message-specific override>);
    the envelope's own generic `data` property matches first, so the
    concrete object schema (the one with `properties`) wins when present."""
    payload = deref(message.get("payload") or {}, root)
    subs = payload.get("allOf", [payload]) if isinstance(payload, dict) else []
    fallback = None
    for sub in subs:
        sub = deref(sub, root)
        props = (sub.get("properties") or {}) if isinstance(sub, dict) else {}
        if "data" not in props:
            continue
        cand = deref(props["data"], root)
        if isinstance(cand, dict) and "properties" in cand:
            return cand
        if fallback is None:
            fallback = cand
    return fallback


def type_set(schema):
    t = schema.get("type") if isinstance(schema, dict) else None
    if isinstance(t, list):
        return {str(v) for v in t}
    return {str(t)} if isinstance(t, str) else set()


def narrowed(label, base, head):
    """Yield narrowing problems for one data field."""
    bt, ht = type_set(base), type_set(head)
    if bt and ht and ht < bt:
        yield f"{label}: type narrowed {sorted(bt)} -> {sorted(ht)}"
    be, he = base.get("enum"), head.get("enum")
    if be is None and he is not None:
        yield f"{label}: enum added ({len(he)} values) where any value was valid"
    elif be is not None and he is not None and set(map(repr, he)) < set(map(repr, be)):
        yield f"{label}: enum narrowed {len(be)} -> {len(he)} values"


def breaking(base, head):
    """The list of breaking-change descriptions between two parsed specs."""
    problems = []
    for ch in sorted(base.get("channels") or {}):
        bch = base["channels"][ch]
        if ch not in (head.get("channels") or {}):
            problems.append(f"channel {ch} (address {bch.get('address', '?')}) removed")
            continue
        bmsgs, hmsgs = bch.get("messages") or {}, (head["channels"][ch].get("messages") or {})
        for m in sorted(bmsgs):
            if m not in hmsgs:
                problems.append(f"message {ch}/{m} removed")
                continue
            try:
                bdata = data_schema(deref(bmsgs[m], base), base)
                hdata = data_schema(deref(hmsgs[m], head), head)
            except (KeyError, ValueError):
                problems.append(f"message {ch}/{m} no longer resolvable (definition removed)")
                continue
            if not isinstance(bdata, dict) or not isinstance(hdata, dict):
                continue
            label = f"{ch}/{m}.data"
            breq = set(bdata.get("required") or [])
            hprops = hdata.get("properties") or {}
            hreq = set(hdata.get("required") or [])
            for f in sorted(breq):
                if f not in hprops:
                    problems.append(f"{label}.{f}: required field removed or renamed")
                elif f not in hreq:
                    problems.append(f"{label}.{f}: no longer required (old readers break when it is omitted)")
            for f in sorted(set(bdata.get("properties") or {}) & set(hprops)):
                problems.extend(narrowed(f"{label}.{f}", deref(bdata["properties"][f], base),
                                         deref(hprops[f], head)))
    return problems


def run(base_path, head_path):
    with open(base_path) as f:
        base = yaml.safe_load(f)
    with open(head_path) as f:
        head = yaml.safe_load(f)
    problems = breaking(base, head)
    if problems:
        print("BREAKING AsyncAPI changes vs the base branch:")
        for p in problems:
            print(f"  - {p}")
        print(CONSUMERS_NOTE)
        print("Make the change additive, or ship it as a new .v2 dataschema/type "
              "(see the envelope contract in apis/asyncapi.yaml); then the consumers "
              "refresh their pins deliberately, never by surprise.")
        return 1
    print(f"asyncapi: no breaking change ({len(base.get('channels') or {})} channels checked). " + CONSUMERS_NOTE)
    return 0


def self_check():
    """Minimal proof the four rules fire and additive changes pass.

    The fixture mirrors the real spec's AsyncAPI 3.0 shape: channel-level
    messages maps whose entries $ref components/messages (one message keeps
    an INLINE payload, so both resolution paths are exercised)."""
    base_yaml = """
channels:
  events:
    address: warehouse.yard-management.events
    messages:
      VisitCheckedIn: { $ref: '#/components/messages/VisitCheckedIn' }
      TrailerDocked:
        payload:
          allOf:
          - $ref: '#/components/schemas/CloudEventEnvelope'
          - type: object
            properties:
              data: { $ref: '#/components/schemas/TrailerDockedData' }
components:
  messages:
    VisitCheckedIn:
      payload:
        allOf:
        - $ref: '#/components/schemas/CloudEventEnvelope'
        - type: object
          properties:
            data: { $ref: '#/components/schemas/VisitCheckedInData' }
  schemas:
    CloudEventEnvelope:
      type: object
      required: [specversion, id, data]
      properties:
        specversion: { type: string }
        data: { type: object }
    VisitCheckedInData:
      type: object
      required: [visit_id, site_id]
      properties:
        visit_id: { type: string }
        site_id: { type: string }
        carrier_flag: { type: [string, 'null'], enum: [SUSPENDED, null] }
        notes: { type: string }
    TrailerDockedData:
      type: object
      required: [trailer_id]
      properties:
        trailer_id: { type: string }
"""
    def head(edit):
        doc = yaml.safe_load(base_yaml)
        edit(doc)
        return doc

    def expect(edit, want, case):
        problems = breaking(yaml.safe_load(base_yaml), head(edit))
        got = "\n".join(problems)
        assert want in got, f"{case}: expected '{want}' in:\n{got}"
        assert problems, f"{case}: expected problems"

    expect(lambda d: d["channels"]["events"]["messages"].pop("VisitCheckedIn"),
           "message events/VisitCheckedIn removed", "removed message")
    expect(lambda d: d["components"]["schemas"]["VisitCheckedInData"]["required"].remove("site_id"),
           "site_id: no longer required", "un-required field")
    expect(lambda d: d["components"]["schemas"]["VisitCheckedInData"]["properties"].pop("visit_id"),
           "visit_id: required field removed", "removed field")
    expect(lambda d: d["components"]["schemas"]["VisitCheckedInData"]["properties"]["carrier_flag"].update(type="string"),
           "carrier_flag: type narrowed", "narrowed type")
    def dangle_ref(d):
        msg = d["channels"]["events"]["messages"]["TrailerDocked"]
        msg["payload"]["allOf"][1]["properties"]["data"]["$ref"] = "#/components/schemas/NoSuchData"
    expect(dangle_ref, "no longer resolvable", "dangling data $ref")
    expect(lambda d: d["channels"].pop("events"),
           "channel events", "removed channel")
    def head_additive(d):
        data = d["components"]["schemas"]["VisitCheckedInData"]
        data["properties"]["gate_lane"] = {"type": ["string", "null"]}  # new OPTIONAL field
        data["properties"]["site_id"]["type"] = ["string", "null"]      # widening: not breaking
        d["channels"]["events"]["messages"]["TrailerArrived"] = {"payload": {}}  # new message
        d["channels"]["events"]["description"] = "edited"               # doc edit
    additive = head(head_additive)
    assert not breaking(yaml.safe_load(base_yaml), additive), "additive change flagged"
    real_spec_check()
    print("asyncapi_breaking self-check: 6 breaking cases fire on the 3.0 fixture, additive "
          "changes pass, and the real apis/asyncapi.yaml resolves.")
    return 0


def real_spec_check():
    """The gate must read THIS repo's real 3.0 spec: every channel-level message
    resolves a concrete data schema (else a message could silently go unchecked)
    and the spec against itself is silent."""
    here = os.path.dirname(os.path.abspath(__file__))
    path = os.path.join(here, "..", "..", "apis", "asyncapi.yaml")
    if not os.path.exists(path):
        return
    with open(path) as f:
        doc = yaml.safe_load(f)
    seen = 0
    for ch, body in (doc.get("channels") or {}).items():
        for name, msg in (body.get("messages") or {}).items():
            data = data_schema(deref(msg, doc), doc)
            assert isinstance(data, dict) and isinstance(data.get("properties"), dict), \
                f"{ch}/{name}: data schema did not resolve through allOf/$ref"
            seen += 1
    assert seen, "the real spec has no channel-level messages (shape not 3.0?)"
    assert not breaking(doc, doc), "the real spec flags itself"


if __name__ == "__main__":
    if len(sys.argv) == 2 and sys.argv[1] == "--self-check":
        sys.exit(self_check())
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(2)
    sys.exit(run(sys.argv[1], sys.argv[2]))
