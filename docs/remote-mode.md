# Remote mode (`forward-mcp serve-http`)

Remote mode serves MCP over **Streamable HTTP** for many authenticated callers
(agents, humans) at once. It is the only path agents use to reach Forward.
Stdio mode (`forward-mcp` with no subcommand) is unchanged.

```sh
export FORWARD_API_BASE_URL=... FORWARD_API_KEY=... FORWARD_API_SECRET=...
export FORWARD_REMOTE_STATIC_TOKENS='agent-token-0123456789=agent-troubleshoot:predict:162112,gary-token-0123456789=gary'
forward-mcp serve-http --addr 0.0.0.0:8080
curl -s localhost:8080/healthz
```

Endpoints: `POST/GET/DELETE /mcp` (MCP), `GET /healthz`.

## What is exposed

| Serve mode | read | propose | write |
|---|---|---|---|
| stdio | ✔ | ✔ | ✔ (3.0.0 behaviour) |
| remote (default) | ✔ | ✘ | ✘ |
| remote + `FORWARD_ENABLE_PROPOSE_TOOLS=true` | ✔ | ✔ (caller needs `predict` scope) | ✘ |

Write-class tools — Forward mutations (`create_network`, `delete_snapshot`,
locations…) and shared local state (`set_default_network`, `clear_cache`,
memory-graph mutators, index rebuilds) — are never registered in remote mode.
This is not configurable. The full table is `toolpolicy.DefaultCatalog`.

Every propose call is audit-logged as one line:

```
AUDIT {"ts":"…","kind":"propose_call","tool":"predict_run_and_wait","subject":"agent-troubleshoot","issuer":"static","network_id":"162112","change_set_id":"CHG-101","outcome":"ok","duration_ms":41873}
```

Identifiers only; anything that looks like a credential is redacted.

## Identity

The bearer token on every request is verified by a `ports.IdentityVerifier`
and becomes a `Caller{Subject, Issuer, Scopes, NetworkIDs}` visible to tool
handlers via `ports.CallerFromContext`. Missing or bad token → `401`;
tool denied by policy → MCP tool error with the reason.

| `FORWARD_REMOTE_AUTH` | Verifier | Use |
|---|---|---|
| `static` (default) | `FORWARD_REMOTE_STATIC_TOKENS` = `token=subject[:scope1+scope2[:net1+net2]],…` (tokens ≥ 16 chars) | development, S2 spike, isolation tests |
| `none` | requires `FORWARD_REMOTE_ALLOW_ANONYMOUS=true`; every caller is `local` | lab only |
| `jwt` | arrives in M1c.1 (issuer, audience, exp, alg allowlist, JWKS or static key) | production |

Scopes in use: `predict` (required for propose tools). Network ids on a
caller, when present, restrict which networks it may query (enforced in M1c.5).

## Isolation (M1c.3 — in progress)

Cache, memory graph and bloom indexes are partitioned by
`{instance_id}:{subject}:{network_id}` so one caller can never read another's
results. Tests in `internal/service` prove it.

## Deployment

The Dockerfile builds a CGO (SQLite) binary on Go 1.25, runs as a non-root
user, ships `spec/` (certified library) in the image, exposes 8080 and runs
`serve-http` by default. Mount or set `HOME` for persistent SQLite state.

kagent `RemoteMCPServer` (M2):

```yaml
apiVersion: kagent.dev/v1alpha2
kind: RemoteMCPServer
metadata: {name: forward-mcp}
spec:
  url: http://forward-mcp.netorch.svc:8080/mcp
  headersFrom:
    - name: Authorization
      valueFrom: {type: Secret, name: forward-mcp-agent-token, key: authorization}  # "Bearer <token>"
```

## Other settings

| Env | Default | Meaning |
|---|---|---|
| `SERVER_HOST` / `SERVER_PORT` | `0.0.0.0` / `8080` | listen address (or `--addr`) |
| `FORWARD_REMOTE_STATELESS` | `false` | disable MCP session tracking (load balancers without sticky sessions) |
| `FORWARD_CERTIFIED_LIBRARY_PATH` | `spec/certified` | certified NQE library directory |
