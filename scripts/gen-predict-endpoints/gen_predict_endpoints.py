#!/usr/bin/env python3
"""Generate docs/predict-endpoints.md from forward-python-sdk spec/unpublished.yaml.

Usage: gen_predict_endpoints.py <sdk_dir> <out_md>
"""
import json
import subprocess
import sys

import yaml

WANT = [
    # change set lifecycle
    "createChangeSet", "getChangeSetSummary", "getChangeSetDraft",
    "setChangeSetDeviceCommands", "validateChangeSetDeviceCommands",
    "commitChangeSet", "getChangeSetHeadCommit", "listChangeSetCommits",
    "predictChangeSet", "listPredictedSnapshots",
    "deleteChangeSet", "deleteChangeSets",
    # firewall predict
    "getSecurityRulesDiff", "addSecurityRule", "updateSecurityRule", "deleteSecurityRule",
    # results: connectivity + counts
    "getSubnetConnectivityDiff", "getVulnerabilityDiffCounts", "getRoutingLoopDiffCount",
    "getDeviceDiffCount", "getInterfaceDiffCount", "getTopologyDiffCount", "getL2DiffCount",
    "getAclDiffCount", "getNatDiffCount", "getArpDiffCount", "getMacDiffCount",
    "getFileDiffCount", "getCheckDiffCounts",
    # results: detail
    "getDeviceDiffs", "getFileDiffs", "getDeviceInterfaceDiffs", "getDeviceAclDiffs",
    "getCheckDiffs", "computeNqeCheckDiffs",
    # base snapshot hygiene
    "reprocessSnapshot",
]

GROUPS = [
    ("Change-set lifecycle", WANT[0:12]),
    ("Firewall (PAN-OS) rule edits", WANT[12:16]),
    ("Results: connectivity and per-area counts", WANT[16:29]),
    ("Results: per-area detail", WANT[29:35]),
    ("Base snapshot hygiene", WANT[35:]),
]


def main(sdk_dir, out):
    spec = yaml.safe_load(open(f"{sdk_dir}/spec/unpublished.yaml"))
    sha = subprocess.check_output(["git", "-C", sdk_dir, "rev-parse", "HEAD"], text=True).strip()
    date = subprocess.check_output(["git", "-C", sdk_dir, "log", "-1", "--format=%cs"], text=True).strip()
    schemas = dict(spec["components"]["schemas"])
    # Unpublished schemas may $ref published ones (e.g. SnapshotInfo).
    published = json.load(open(f"{sdk_dir}/spec/forward-openapi-3.1.json"))
    for k, v in published.get("components", {}).get("schemas", {}).items():
        schemas.setdefault(k, v)

    ops = {}
    for path, methods in spec["paths"].items():
        for m, o in methods.items():
            if isinstance(o, dict) and o.get("operationId") in WANT:
                ops[o["operationId"]] = (m.upper(), path, o)

    used_schemas = []

    def ref_name(s):
        if isinstance(s, dict) and "$ref" in s:
            return s["$ref"].rsplit("/", 1)[-1]
        return None

    def collect(s):
        n = ref_name(s)
        if n and n not in used_schemas:
            used_schemas.append(n)
            if n in schemas:
                collect(schemas[n])
        if isinstance(s, dict):
            for v in s.values():
                collect(v)
        elif isinstance(s, list):
            for v in s:
                collect(v)

    def schema_str(s):
        n = ref_name(s)
        if n:
            return f"`{n}`"
        if not isinstance(s, dict):
            return "`?`"
        t = s.get("type", "")
        if t == "array":
            return f"array of {schema_str(s.get('items', {}))}"
        if t == "object" and "properties" in s:
            return "object {" + ", ".join(f"{k}: {schema_str(v)}" for k, v in s["properties"].items()) + "}"
        if t == "string" and "enum" in s:
            return "enum[" + "|".join(map(str, s["enum"])) + "]"
        return f"`{t or 'any'}`"

    lines = []
    w = lines.append
    w("# Forward Predict — HTTP contract (reference for forward-mcp M1b)")
    w("")
    w("**Generated** from `forwardnetworks/forward-python-sdk` `spec/unpublished.yaml` "
      f"at commit `{sha}` ({date}) by `scripts/gen-predict-endpoints` — do not hand-edit the tables; "
      "edit the *Semantics* section only.")
    w("")
    w("All paths are relative to the server base `/api` (same base as the published API). "
      "These endpoints are marked `x-forward-stability: unpublished` by Forward: they may change without notice. "
      "Spec task 1b.5: any live response that disagrees with this document is a **DIVERGENCE** — stop and report, "
      "do not adapt silently.")
    w("")
    w("Conventions that differ from the published API (from the SDK's own notes):")
    w("- a query-string `action=` selects the operation, so predict and delete share a path;")
    w("- Forward rejects unknown JSON body properties with HTTP 400, so bodies carry exactly the documented keys;")
    w("- device command / config bodies are `text/plain; charset=utf-8`, not JSON; max 2 MiB.")
    w("")

    for title, names in GROUPS:
        w(f"## {title}")
        w("")
        for name in names:
            m, path, o = ops[name]
            w(f"### `{name}` — `{m} {path}`")
            if o.get("summary"):
                w("")
                w(o["summary"])
            if o.get("description"):
                w("")
                w(o["description"].strip())
            params = [p for p in o.get("parameters", []) if p["in"] != "path"]
            if params:
                w("")
                w("| Param | In | Required | Type | Notes |")
                w("|---|---|---|---|---|")
                for p in params:
                    w(f"| `{p['name']}` | {p['in']} | {'yes' if p.get('required') else 'no'} | {schema_str(p.get('schema', {}))} | {p.get('description', '').strip().replace(chr(10), ' ')} |")
            rb = o.get("requestBody")
            if rb:
                for ct, c in rb["content"].items():
                    s = c.get("schema", {})
                    collect(s)
                    w("")
                    w(f"**Request body** (`{ct}`, {'required' if rb.get('required') else 'optional'}): {schema_str(s)}")
                    if "example" in c:
                        w("")
                        w("```json")
                        w(json.dumps(c["example"], indent=2))
                        w("```")
            for code, r in o.get("responses", {}).items():
                w("")
                w(f"**Response {code}**: {r.get('description', '').strip()}")
                for ct, c in (r.get("content") or {}).items():
                    s = c.get("schema", {})
                    collect(s)
                    w("")
                    w(f"`{ct}` → {schema_str(s)}")
                    if "example" in c:
                        w("")
                        w("```json")
                        w(json.dumps(c["example"], indent=2))
                        w("```")
            w("")

    w("## Schemas referenced above")
    w("")
    for n in used_schemas:
        if n not in schemas:
            w(f"### `{n}`\n\n(not found in either spec file)\n")
            continue
        s = schemas[n]
        w(f"### `{n}`")
        if s.get("description"):
            w("")
            w(s["description"].strip())
        props = s.get("properties")
        if props:
            req = set(s.get("required", []))
            w("")
            w("| Field | Type | Required | Notes |")
            w("|---|---|---|---|")
            for k, v in props.items():
                w(f"| `{k}` | {schema_str(v)} | {'yes' if k in req else 'no'} | {str(v.get('description', '')).strip().replace(chr(10), ' ') if isinstance(v, dict) else ''} |")
        elif "enum" in s:
            w("")
            w("enum: " + ", ".join(f"`{e}`" for e in s["enum"]))
        else:
            w("")
            w(schema_str(s))
        w("")

    open(out, "w").write("\n".join(lines))
    print(f"wrote {out}: {len(ops)} operations, {len(used_schemas)} schemas")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
