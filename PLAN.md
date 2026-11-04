# PLAN.md — Agentic NetOps stack on Forward (spec v0.1, 2026-11-04)

Status: v0.4 — 2026-11-04. Done: M1.0, M1a, M1b (fixture-tested, PR #7), M1c except JWT, E.1–E.3. **v0.4 changes the platform (§1b): everything outside forward-mcp is built natively in Go on a single-node k0s — no kagent, no Temporal, no OpenShell.** Q3a/Q9 resolved.
Repo inspected: `gaberger/forward-mcp` @ `be5a7ab` (main, "Untrack local tool caches", 2026-06-11).
Working branch: `feat/netorch-m1` in `gaberger/forward-mcp`, kept upstream-able. Agents / eval runner / Temporal go in a separate `netorch` repo.

Changes in v0.2: D2 replaced by the hex-as-you-go rule (§1), Q1/Q5 resolved, new §1a Gaps, new D7 (Go, not Rust).
Changes in v0.4: §1b native platform (D8–D10), M2/M3/S1/S4 rewritten for it, Q3/Q9 resolved, netorch layout updated.

---

## 0. What the repo actually is (findings that change the plan)

| # | Finding | Consequence |
|---|---------|-------------|
| F1 | **stdio only.** `cmd/server/main.go` runs `server.Run(ctx, &mcp.StdioTransport{})`. There is no HTTP listener, no auth, no "remote mode". `ServerConfig{Port,Host}` exists in config but is unused. | M1c ("remote mode accepts a JWT per caller") first needs a remote transport. go-sdk v1.6.1 ships `mcp.NewStreamableHTTPHandler`, so this is additive, not a rewrite, but it is a real new task (M1c.0 below). |
| F2 | **Partitioning is per Forward instance, not per caller.** `instance_id = SHA-256(host)[:16]`; cache / memory / bloom keys are `{instance_id}:{...}`. One process = one API key = one tenant. | Spec non-negotiable #5 needs a second partition dimension (`subject`, `network_id`) threaded through SemanticCache, MemorySystem and BloomIndexManager, plus a request-scoped identity. |
| F3 | **Write tools already exist against the Forward API**: `create_network`, `update_network`, `delete_network` (client only), `delete_snapshot`, `create_location`, `create_locations_bulk`, `update_location`, `delete_location`, `update_device_locations`, plus memory-graph mutators (`create_entity`, `delete_entity`, …) and `clear_cache`. Annotated via `toolAnnotations` in `mcp_adapter.go` (advisory hints only). | Non-negotiable #1 ("write tools do not exist in this phase") needs an **enforced** tool-class registry: every tool tagged `read` / `propose` / `write`, and remote mode registers only `read` (+ `propose` when flagged). Stdio mode keeps today's behaviour so existing users are not broken. |
| F4 | **Hex refactor (ADR-001) accepted but not executed.** `internal/ports` and `internal/domain/entities` exist but nothing imports them; service still depends on `forward.ClientInterface`; there is no `usecases/` or `adapters/`; `docs/workplans/hex-arch-refactor.json` lists every task as pending. `mcp_service.go` is ~4.4k lines. | **Gary's decision (2026-11-04): build M1 in hex, migrate existing code as it is touched.** Rule in §1. |
| F5 | **Test style**: stdlib `testing` + in-package `MockForwardClient` (`mcp_service_test.go`); one file uses testify (already in go.mod). Integration tests are named `TestIntegration*` and excluded with `-skip 'TestIntegration'`; `make test-integration` runs them against `.env`. Race-aware tests via `//go:build race` constant. | New tests follow this exactly. Spec's `FORWARD_LIVE=1` is honoured **and** live tests are named `TestIntegration*` so existing Make targets keep working. |
| F6 | **Release process**: `serverVersion` const in `cmd/server/main.go` + README "Version" line + CHANGELOG entry. CHANGELOG has an `[Unreleased]` section and no `[3.0.0]` entry despite README/main.go saying 3.0.0. No CI (`.github/` absent), no tags visible in shallow clone. `make lint` = golangci-lint. | Each milestone ships as a CHANGELOG `[Unreleased]` entry; version bump to **3.1.0** at M1 completion (additive, backward compatible). I will add the missing 3.0.0 entry as housekeeping (Q7). |
| F7 | **Dockerfile is stale**: `golang:1.21`, `CGO_ENABLED=0` (breaks go-sqlite3), `EXPOSE 8080` with nothing listening. | Must be fixed for remote mode / kagent (M1c.0). |
| F8 | **Existing building blocks to reuse**: `search_nqe_queries` (semantic index over 6k+ queries, keyword/OpenAI embeddings), `run_nqe_query_by_id`, `DiffNQEQuery`, `get_config_diff`, `search_paths` / `search_paths_bulk`, `list_snapshots`, `get_latest_snapshot`, `NQEDatabase` (SQLite, out-of-process hydrate). | Certified library is a thin, versioned, curated layer on top of the existing index — not a replacement. |
| F9 | **Predict reference** (`forward-python-sdk/docs/predict.md`, read 2026-11-04): lifecycle is *create change set → set_commands(device, cli text) / firewall rule edits → commit(note) → predict(note) → new PREDICT snapshot → snapshot_diffs / nqe.diff(before, after)*. Raw HTTP paths are **not** in the doc; they must be extracted from SDK source. Results carry `is_partial_result` / `complete:false` / `total_subnet_pairs==0` semantics that are *not* "pass". | Spec tool names map cleanly, but `predict_run_and_wait` must commit then predict, and `predict_get_results` must surface partial/incomplete as **UNKNOWN**, never PASS. Endpoint extraction is task 1b.0. |
| F10 | **Sandbox can't build this repo** (local Go 1.24.7, module wants 1.25, no module proxy). | I can write code and tests but cannot run `make test` here. Need Gary's machine or a CI job (Q6). |

---

## 1. Repo layout and the hex rule

Two repos (confirmed):

- **`forward-mcp`** (this repo, Go, branch `feat/netorch-m1`): M1 entirely, plus the eval *grader* (deterministic re-run of queries) as `cmd/eval-grader`.
- **`netorch`** (new): kagent Agent resources + skill packs, Helm/kustomize, eval *runner* + replay cases, Temporal worker/workflows, trigger receiver, spike notes. Go (D7), YAML for kagent.

**Hex rule (replaces old D2).** Every new capability is built under the ADR-001 layout — a domain entity, a port interface, a use case, and an adapter — never as a standalone package with its own ad-hoc interface. Existing code is migrated *when touched at a seam*: if a task needs the Forward client, the cache or the memory graph, that dependency is moved behind its port (`internal/ports`) in the same PR rather than left as a concrete type. The pending refactor is not done as a separate M0; it is drained by M1.

```
internal/
  domain/
    entities/      # existing + ToolClass, CertifiedQuery, Evidence, Caller, PredictResult
    services/      # pure rules: tool policy, answerability scoring, predict verdict
  ports/           # existing forward.go, cache.go + toolregistry.go, certified.go,
                   #   identity.go, predict.go, auditlog.go
  usecases/        # RegisterToolsForMode, SearchCertified, CheckAnswerable,
                   #   RunCertified, PredictRoundTrip
  adapters/
    mcp/           # go-sdk registration (moves addTool/toolAnnotations out of service)
    certifiedfs/   # spec/certified/ file loader
    jwt/           # token verifier                                   (M1c)
    httpserver/    # Streamable HTTP transport + auth middleware       (M1c.0)
    predicthttp/   # Predict HTTP client on forward.Client plumbing    (M1b)
    predictfixture/# recorded-fixture Predict client                   (M1b)
  service/         # existing orchestrator; shrinks as seams migrate
spec/certified/    # versioned NQE library: VERSION, queries/*.json, fixtures/*.json
cmd/eval-grader/   # deterministic grader CLI                           (Eval)
docs/predict-endpoints.md, docs/certified-library.md, docs/remote-mode.md
```

## 1b. Platform decision (v0.4): native on k0s

Gary's direction (2026-11-04): *build all native, no external dependencies; k0s as the runtime; small vetted Go modules are fine.* Concretely:

| Spec component | v0.1–0.3 plan | v0.4 |
|---|---|---|
| Agent runtime | kagent v1 on Agent Substrate, Claude harness, A2A | **netorch `agentd`** (Go): Claude Messages API over `net/http`, MCP client from the go-sdk already in use, skill packs as versioned prompt files, `POST /agents/{name}/ask` + SSE. One binary, one Deployment. |
| Change workflow | Temporal | **netorch `flowd`** (Go): durable state machine persisted in SQLite (WAL) per workflow, steps = draft → predict → await-approval (days) → execute-stub → done; signals over HTTP; restart-safe by replaying persisted step records. |
| Guardrails | OpenShell egress + credential policy | **Kubernetes NetworkPolicy** on k0s (agent pods may reach only forward-mcp and `api.anthropic.com`) + **netorch `tokend`**: a minimal JWT issuer (RS256, JWKS endpoint) so agents hold short-lived tokens and never a Forward credential. |
| Runtime | kagent's cluster | **k0s** single node (`k0s install controller --single`), kustomize for every netorch component. |
| Triggers | kagent + Temporal schedules | `agentd` webhook receiver (Forward `SNAPSHOT_READY` / `*_VERIFICATION_FAILURE`), cron inside `flowd`. |
| Models | Claude via kagent ModelConfig; local lane via tt-inference-server | Claude via `agentd` directly; local lane = OpenAI-compatible endpoint config in `agentd` (S3 unchanged in spirit). |

Spikes change accordingly: **S1** → does NetworkPolicy on k0s actually block the agent pod from everything but forward-mcp and the model endpoint (test with a probe pod)? **S4** → `flowd` activity calling `agentd` over HTTP: auth, streaming, timeouts, cancellation. S2, S3 unchanged.

Decisions: **D8** native agent runner instead of kagent; **D9** native SQLite-backed workflow engine instead of Temporal; **D10** NetworkPolicy + own token issuer instead of OpenShell. Vetted modules allowed: `github.com/golang-jwt/jwt/v5` (forward-mcp verifier + `tokend`), `modernc.org/sqlite` or `mattn/go-sqlite3` (netorch `flowd`; forward-mcp already uses mattn), OTel SDK when M2 needs it.

## 1a. Gaps — things the spec needs that neither repo has (create or borrow)

| Gap | Create / borrow | Where | When |
|-----|-----------------|-------|------|
| **Trigger receiver** — Forward webhooks (`SNAPSHOT_READY`, `NQE_VERIFICATION_FAILURE`, `INTENT_VERIFICATION_FAILURE`), schedules, Slack/ServiceNow entry points. Nothing receives them. | Create: small Go HTTP service that validates the event, maps it to an agent (A2A) or a Temporal workflow, and starts it. Schedules via Temporal Schedules (borrow). | netorch | M2 (webhook → Troubleshoot), M3 (→ Change) |
| **JWT issuer for agents** | Borrow if Agent Substrate / kagent mints tokens (Q9); otherwise create a minimal issuer (static key in a Secret) for the lab. | netorch | before M1c live tests |
| **Temporal → kagent A2A bridge** | Borrow kagent's Go A2A client if one exists; else create a thin client. S4 decides. | netorch | S4 |
| **Predict HTTP client** | Port from `forward-python-sdk` (no Go client exists). | forward-mcp `adapters/predicthttp` | M1b |
| **Approval adapter** (Slack or ServiceNow) | Create; CLI stub first. | netorch | M3 (Q4) |
| **OTel wiring** in forward-mcp and agents | Borrow `go.opentelemetry.io/otel` (Q3b). | both | M2 |
| **Secret store** beyond env vars | Borrow (K8s Secrets via kagent; external store later). | netorch manifests | M2 |
| **Remote transport** for forward-mcp | Create on go-sdk `StreamableHTTPHandler`. | forward-mcp | M1c.0 |
| **CI** | Create GitHub Actions. | both | M1 |

---

## 2. Milestones and tasks

Task IDs are stable; estimates are engineering days, serial.

### M1.0 — Tool classes and mode gate (prerequisite for everything) — 1.5d
| ID | Task | Done when |
|----|------|-----------|
| 1.0.1 | `domain/entities.ToolClass{Read,Propose,Write}` + `ToolSpec`; `domain/services/toolpolicy`: pure `Policy` rules (stdio = all, remote = read, remote+flag = read+propose, write never remote); `ports/toolregistry.go` port. Table mapping every one of the 54 tools to a class (derived from `toolAnnotations`, then hand-reviewed; memory-graph mutators = `write`, `set_default_network` = `read`-safe session state). | Unit test: every registered tool has a class; table and `toolAnnotations` agree (readOnly ⇔ `read`). |
| 1.0.2 | `adapters/mcp`: registration moves out of `service` (`addTool`, `toolAnnotations`) behind the port; `usecases.RegisterToolsForMode(policy)`. `FORWARD_ENABLE_PROPOSE_TOOLS=true` adds `Propose` in remote mode. `Write` can never be enabled in remote mode (hard-coded, not config). | Unit test: remote policy never lists a write tool; stdio policy lists all 54. |
| 1.0.3 | `ports/auditlog.go` + adapter on the existing logger: structured line `{ts, tool, caller.subject, network_id, change_set_id, outcome}` for every propose call. | Unit test asserts log line and asserts no token/secret substrings appear. |

### M1a — Certified NQE library — 4d
| ID | Task | Done when |
|----|------|-----------|
| 1a.1 | Schema (`spec/certified/schema.json`) + Go types: `id, version, title, intent, synonyms[], parameters[]{name,type,required,description}, output_schema (JSON Schema), owner, certified bool, nqe_query_id OR nqe_path, fixture`. Library dir has `VERSION` file (semver) and a `CHANGELOG`. | Loader rejects malformed entries with path+field in the error; golden test on 3 sample entries. |
| 1a.2 | Seed library: ~15 queries needed by M2 skill pack and Eval (device inventory, interface status, BGP neighbors down, ACL hits for 5-tuple, route lookup, VLAN membership, OSPF adjacency, NTP/DNS/syslog config, config change list between snapshots, duplicate IPs, ...). Each maps to an existing Forward org/fwd query by id/path, has a fixture result and output schema. Owner = Gary unless told otherwise. | Each entry's fixture validates against its `output_schema`. |
| 1a.3 | `search_certified_queries(question, limit)` → ranked `[{id, title, score, why}]`. Ranking = existing embedding provider (`keyword` default) over `title+intent+synonyms`, boosted by exact synonym hits. Reuses `NQEQueryIndex` machinery; no new dependency. | Unit tests with fixed keyword embeddings: known questions rank expected id first. |
| 1a.4 | `check_answerable(question)` → `{coverage: 0..1, matched_query_ids[], missing[], recommendation: certified|adhoc|unanswerable}`. Coverage = best-match score mapped through calibrated thresholds; `missing` lists parameters the question didn't supply (e.g. "which device?"). | Table test: 10 questions → expected recommendation; thresholds documented. |
| 1a.5 | `run_certified_query(id, params, snapshot_id)` → validates params against entry schema, runs via existing `RunNQEQueryByID`, validates result against `output_schema` (warn, not fail, on drift), returns `{query_id, library_version, snapshot_id, rows, uncertified:false}`. | Unit test with `MockForwardClient`; drift path tested. |
| 1a.6 | Ad-hoc path labelling: `run_nqe_query_by_id` / `run_nqe_query` responses gain `"certified": false` and the evidence envelope `{snapshot_id, query_ref}` so agents can cite either path uniformly. Non-breaking (additive JSON fields). | Existing tests pass; new assertion on field presence. |
| 1a.7 | Evidence envelope type (`internal/certified/evidence.go`) shared with path search: `{kind: nqe|path_search, ref, snapshot_id, library_version?}`. `search_paths*` responses include it. | Unit test. |
| 1a.8 | `docs/certified-library.md`: how to add/certify a query, review rule (owner + fixture + schema = certified). | — |

### M1c — Identity, remote mode, isolation — 5d (ordered before 1b per spec)
| ID | Task | Done when |
|----|------|-----------|
| 1c.0 | **Remote transport.** `forward-mcp serve-http` subcommand (keeps stdio as default): `mcp.NewStreamableHTTPHandler`, bind from `ServerConfig{Host,Port}`, `/healthz`, graceful shutdown reusing existing errgroup pattern. Fix Dockerfile (go 1.25, `CGO_ENABLED=1`, alpine + sqlite libs, non-root). | `go test` spins server on ephemeral port and completes initialize + `list_tools` through go-sdk client. Docker image builds. |
| 1c.1 | `adapters/identity/jwt`: verifier over `golang-jwt/jwt/v5`: issuer + audience + exp + alg allowlist (RS256/ES256; HS256 only for lab), keys from a JWKS URL (netorch `tokend`) or static PEM via env; claims `sub`, `scope`, `network_ids` → `Caller`. `FORWARD_REMOTE_AUTH=jwt`. | Unit tests: valid, expired, wrong-aud, alg=none, wrong key → 401; claims mapped. |
| 1c.2 | **Done via method expressions:** handlers are registered as `(*ForwardMCPService).handler` and the registrar invokes each call on `forCaller(caller)`, a view whose stores are the caller's partitions. Legacy handlers needed no signature change; new handlers take `ctx` and use `ports.CallerFromContext`. | `TestLocalCallerGetsTheUnscopedService` |
| 1c.3 | **Done, differently than sketched:** instead of a new `subject` column, the caller's partition id `<instance>-<sha256(subject)[:16]>` replaces the instance id in every instance-scoped store (memory graph via `MemorySystem.ForInstance` view, bloom managers per partition, API tracker), and the semantic cache carries the subject in keys and similarity scans. Network stays the second key where the data is network-bound (cache key; memory records carry it) and the caller allow-list is enforced at the tool boundary (1c.5). No schema migration needed; 3.0.0 data stays readable for the local caller. | `isolation_test.go` |
| 1c.4 | **Isolation tests** (spec acceptance): two callers A/B, same network: A caches a result; B `Get` misses; B `FindSimilarQueries` returns nothing of A's; memory `search_entities` by B returns none of A's; same for bloom. Also A on network N1 vs A on N2. Run with `-race`. | All green under `go test -race`. |
| 1c.5 | Network scoping: if JWT carries `network_ids`, any tool call for another network → 403-style tool error ("caller not authorised for network X"). | Unit test. |
| 1c.6 | Secret hygiene: redaction filter in logger (API key/secret/JWT patterns → `[REDACTED]`); tokens read from env only; `get_default_settings` and error paths never echo credentials. Grep-style unit test over captured logs from a full tool-call cycle. | Test green. |
| 1c.7 | Rate limit per subject (token bucket, env-configurable, default generous) — needed for S2, cheap to add now. | Unit test. |
| 1c.8 | `docs/remote-mode.md`: env vars, JWT claims contract, kagent `RemoteMCPServer` snippet. | — |

### M1b — Predict tools (propose class) — 5d
| ID | Task | Done when |
|----|------|-----------|
| 1b.0 | Extract HTTP contract from `forward-python-sdk` source (change_sets create/commit/predict, device command set/validate, firewall_predict rules diff/add, snapshot_diffs counts + per-area, nqe.diff, wait semantics) into `docs/predict-endpoints.md` with method/path/body/response per call and the SDK commit hash it was read from. | Doc reviewed by Gary before 1b.2. |
| 1b.1 | `internal/predict`: single `Client` interface: `CreateChangeSet, StageCLI, StageFirewallRule, ValidateCLI, Commit, Predict, WaitProcessed, ConnectivityDiff, AreaCounts, AreaDetail, NQEDiff, Delete`. Result model: `Result{Status: PASS|FAIL|UNKNOWN, PredictSnapshotID, BaseSnapshotID, Connectivity{NewlyIsolatedPairs, TotalPairs, Partial}, Areas map[string]{Count, Complete}, Diffs..., Reasons []string}`. Status rules: any `Partial`/`!Complete` → UNKNOWN; `TotalPairs==0` → UNKNOWN with reason; newly isolated > 0 → FAIL; else PASS. | Unit tests over the status rules (table-driven). |
| 1b.2 | HTTP implementation on the existing `forward.Client` plumbing (`makeRequestWithRetry`, TLS 1.3, credential zeroing). Plain-text body for `set_commands`. | Contract tests (1b.4) green against fixtures. |
| 1b.3 | MCP tools (all class `propose`, hidden unless flag): `predict_create_change_set(network_id, base_snapshot_id, description)`; `predict_stage_edits(change_set_id, device, edits)` where `edits` = `{cli: "..."}` or `{firewall_rules:[...]}` (PAN-OS CLI `set` text rejected up front per F9 caveat); `predict_run_and_wait(change_set_id, timeout_s)` = commit + predict + wait; `predict_get_results(change_set_id)` → `Result` JSON. Every call audit-logged (1.0.3). Change-set → caller binding: a caller may only operate on change sets it created (in-memory + SQLite table). | Unit tests with fixture client; flag-off test proves tools absent from `list_tools`. |
| 1b.4 | **Contract tests**: `internal/predict/contract_test.go` runs the same scenario twice — `FORWARD_LIVE=1` (and named `TestIntegrationPredict*`) hits a live instance; otherwise replays `testdata/predict/*.json` recorded with a `-record` flag (redacts host/token). Scenario: create → stage one harmless CLI edit → commit → predict → wait → results → delete. | Fixture run green in `make test`; live run green once sandbox access exists. |
| 1b.5 | Divergence protocol: any live response that fails to decode or returns a status the reference doesn't list → test fails with a `DIVERGENCE:` report (endpoint, expected, observed) written to `docs/predict-divergences.md`. Per spec: **stop and report**, do not adapt silently. | Mechanism tested with a synthetic divergent fixture. |
| 1b.6 | Cleanup: change sets created by tests/agents are tagged in the description (`[netorch:<subject>:<run-id>]`) and a `predict_gc` maintenance path deletes stale ones (cascade deletes their PREDICT snapshots — documented). | Unit test. |

### Eval harness (built alongside M1) — 3d
| ID | Task | Done when |
|----|------|-----------|
| E.1 | Case schema (`netorch/eval/cases/*.yaml`): `{id, network_id, snapshot_before, snapshot_after?, input: {question|alert}, gold: {kind: certified_query|path_search, ref, params, expected: rows|outcome}, expected_verdict: cause|no_network_issue, notes}`. | Loader + 2 sample cases. |
| E.2 | `cmd/eval-grader` (in forward-mcp, Go): takes agent output (M2 contract) + case; for each cited evidence item re-runs the query/path search via forward-mcp (remote, as `eval-grader` identity) and compares to gold (set equality on rows after key normalisation; path outcome equality). Emits `{accuracy, uncited_facts, evidence_matches, tool_calls, tokens, latency}`. "Uncited fact" = any sentence in `summary` with a network noun/IP/device that has no evidence index — deterministic regex pass; LLM grading only for prose quality, behind `--llm`. | Unit tests with canned agent outputs. |
| E.3 | Runner (`netorch/eval/run`): executes N cases against a named agent (A2A, after M2), writes JSONL + markdown report, `--baseline` vs `--candidate` diff table. | Runs end-to-end with a stub agent before M2. |
| E.4 | 10 replayed incidents authored with Gary (needs real snapshots; see Q2). | Cases committed. |

### Spikes S1, S2 (after M1) — 1d each → `docs/spikes/S1-openshell.md`, `S2-remote-concurrency.md`
S2 uses 1c.7 metrics + a Go load tool hitting `serve-http` with 5 JWT subjects; reports isolation (re-run 1c.4 under load), 429 behaviour, cache hit rate per subject.

### M2 — Troubleshoot agent on `agentd` (read-only) — 7d
| ID | Task |
|----|------|
| 2.0 | `netorch/cmd/agentd`: agent loop = Claude Messages API (tool use) ↔ MCP client (go-sdk) against forward-mcp remote; skill pack = system prompt + skill files loaded from `agents/<name>/`; output contract validated as JSON before returning; `POST /agents/{name}/ask` (sync) and `/ask?stream=1` (SSE); per-agent bearer token for forward-mcp from a Secret; OTel traces with caller subject + snapshot ids. |
| 2.1 | Troubleshoot agent definition in `agents/troubleshoot/` (prompt + 3 skills, already drafted) with a read-class tool allowlist enforced by `agentd` on top of forward-mcp's policy. |
| 2.2 | Trigger receiver in `agentd`: Forward webhooks (`SNAPSHOT_READY`, `NQE_VERIFICATION_FAILURE`, `INTENT_VERIFICATION_FAILURE`) → agent ask; HMAC-verified. |
| 2.3 | `deploy/k0s/`: k0s bootstrap script, kustomize for forward-mcp, agentd, tokend; NetworkPolicy for the agent namespace; secrets out of band. |
| 2.4 | `netorch eval run -agent agentd` client (replaces the A2A client). |
| 2.5 | Run Eval on the 10 cases; iterate skill prompts until ≥7/10 with 0 uncited facts; commit baseline report. |

### S4 (after M2) — 1d → `docs/spikes/S4-flowd-agentd.md`: `flowd` calling `agentd` over HTTP — auth, streaming, timeouts, cancellation, retries without duplicate predictions.

### M3 — Change workflow skeleton on `flowd` — 6d
| ID | Task |
|----|------|
| 3.0 | `netorch/cmd/flowd`: durable workflow engine — a workflow is a persisted sequence of step records (SQLite WAL, one row per step with inputs/outputs/evidence); the runner replays completed steps on restart and resumes the first incomplete one; timers persisted; signals (`approve`/`reject`) via `POST /workflows/{id}/signal` with bearer auth; idempotency keys on every outbound call so a crash mid-step never repeats a prediction. |
| 3.1 | `ChangeWorkflow`: draft (Change agent via `agentd`) → predict (forward-mcp propose tools, identity `flowd-change`) → await approval (timer, default 7d) → execute stub (records intent) → done. |
| 3.2 | Approval: CLI (`netorch approve <id>`) first; Slack or ServiceNow adapter per Q4. |
| 3.3 | Tests: happy path; rejection; timeout; **process killed mid-approval and restarted** resumes and still accepts the signal; duplicate-signal and duplicate-step idempotency. |

### S3 (last) — 1d, lab only → `docs/spikes/S3-local-model-toolcalling.md`

---

## 3. Test plan (summary)

| Layer | What | How it runs |
|-------|------|-------------|
| Unit | toolclass table, certified loader/search/answerability/runner, identity/JWT, partition keys, Predict status rules, grader | `make test` / `make test-quick`, `-race` in CI |
| Isolation | 1c.4 cross-identity tests on cache, memory, bloom | `go test -race ./internal/service -run TestIsolation` |
| Transport | in-process Streamable HTTP server + go-sdk client; auth matrix | `make test` |
| Contract | Predict scenario vs recorded fixtures (default) / live (`FORWARD_LIVE=1`, also `TestIntegration*`) | `make test` / `make test-integration` |
| Security | log-redaction assertions; `gosec`, `govulncheck` (already in CLAUDE.md checklist) | `make lint` + new `make security` target |
| Eval | grader unit tests; 10-case replay for M2 acceptance | `netorch eval run` |
| Workflow | Temporal test suite incl. restart-mid-approval | `go test ./workflow/...` |
| Live acceptance (M1) | `check_answerable` + one certified query + Predict round trip on sandbox network | manual, recorded in CHANGELOG |
| CI | GitHub Actions: build (Go 1.25, CGO), `make test-quick -race`, lint, docker build. **New** — repo has none. | on PR |

---

## 4. Decisions I'm proposing (say no to any)

- **D1** Stdio behaviour stays byte-for-byte compatible; all gating applies to `serve-http` (remote) mode. Version bump 3.1.0.
- **D2 (revised)** Hex-as-you-go: all new work under ADR-001 layout; existing code migrated behind ports whenever a task touches it. No separate M0.
- **D3** `propose` tools require **both** `FORWARD_ENABLE_PROPOSE_TOOLS=true` and a JWT scope `predict`. Flag alone is not enough.
- **D4** Predict verdict is three-valued (PASS/FAIL/UNKNOWN); the M3 workflow treats UNKNOWN as not-approved.
- **D5** Certified library lives in-repo under `spec/certified/` (versioned with the server) rather than in Forward's NQE repo, so fixtures and schemas are tested in CI.
- **D6** Eval grader lives in forward-mcp (it is a Forward client); eval runner/cases live in `netorch`.
- **D7** **Go across both repos, not Rust.** forward-mcp is Go and the spec says follow its language; Temporal, kagent A2A and the eval tooling have mature Go SDKs; the Temporal Rust SDK is still immature. Rust is allowed only as an opt-in sidecar if a concrete need appears (e.g. a sandboxed tool runtime), decided case by case.

## 5. Open questions for Gary (blocking marked ★)

| # | Question |
|---|----------|
| ~~Q1~~ | **Resolved:** feature branch `feat/netorch-m1` in `gaberger/forward-mcp`, upstream-able; separate `netorch` repo. |
| Q11 | k0s host: one Linux box/VM (~4 GB) with egress to Forward and `api.anthropic.com` — which one, and who installs k0s? |
| ★Q2 | Forward instance + network for live tests; token with Predict access; are `PREDICT`, `FIREWALL_PREDICT`, `LOCATION_CONNECTIVITY_DIFFS` org properties enabled? Snapshots usable for the 10 eval incidents? |
| ~~Q3~~ | **Resolved (v0.4):** small vetted modules are fine. JWT: `golang-jwt/jwt/v5` (JWKS fetch hand-rolled, it is ~60 lines). OTel SDK in M2. No Temporal. Library files stay JSON. |
| Q4 | Slack vs ServiceNow first for approval (M3). CLI stub ships regardless. |
| ~~Q5~~ | **Resolved:** hex-as-you-go (D2 revised). |
| ~~Q6~~ | **Resolved:** GitHub Actions on both repos, green. |
| Q7 | OK to add the missing `[3.0.0]` CHANGELOG entry and fix the Dockerfile as part of M1c.0? |
| Q8 | M3 persistence: Temporal history only, or also a queryable table (Postgres/SQLite)? |
| ~~Q9~~ | **Resolved (v0.4):** we build `tokend` (RS256, JWKS). |
| Q10 | Who is `owner` on seed certified queries, and should `certified:true` require a second reviewer? |

## 6. Risks

- Predict HTTP contract is unpublished; live behaviour may diverge → protocol in 1b.5, time-boxed; worst case Predict tools ship fixture-tested only in 3.1.0.
- Memory-graph schema migration (adding `subject`) touches a live SQLite file at `~/.forward-mcp/data/memory.db`; migration is additive with `subject='local'` backfill.
- `mcp_service.go` is large and untyped in places; registration-seam changes only, with the full existing suite as regression.
- Certified-query ranking quality with keyword embeddings may be weak for paraphrases; synonyms field + answerability thresholds mitigate; OpenAI provider is already supported if needed.

## 7. Order of execution (per spec)

PLAN.md review → M1.0 → M1a → M1c → M1b (+ Eval E.1–E.3 in parallel) → **M1c.1 JWT + `tokend`** → M1 live acceptance → S1 (NetworkPolicy), S2 → M2 on `agentd` (+E.4) → S4 → M3 on `flowd` → S3.
