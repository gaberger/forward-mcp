# Eval: replay cases and the deterministic grader

A **case** replays one question or alert against recorded snapshots and
states the evidence a correct answer must be grounded in. The **grader**
re-runs each cited piece of evidence and compares it with gold, so a grade
never depends on an LLM's opinion. Only prose quality is LLM-graded (netorch
runner, `--llm`, later).

## Case (`case-<id>.json`)

```json
{
  "id": "bgp-idle-01",
  "network_id": "162112",
  "snapshot_before": "712", "snapshot_after": "715",
  "input": {"question": "Why can't 10.1.1.10 reach 10.2.2.20?"},
  "expected_verdict": "cause",
  "gold": [
    {"kind": "nqe", "ref": "certified:bgp-peerings-not-established@1", "snapshot_id": "712",
     "key_columns": ["device", "neighborAddress", "state"],
     "expected_rows": [{"device": "router-1", "neighborAddress": "192.0.2.2", "state": "IDLE"}]},
    {"kind": "path_search", "ref": "path:10.1.1.10->10.2.2.20", "snapshot_id": "712", "expected_outcome": "DROPPED"}
  ]
}
```

`expected_verdict` is one of `cause`, `no_network_issue`, `insufficient_evidence`.
Gold refs use the evidence vocabulary every forward-mcp result already emits
(`certified:<id>@<v>`, `nqe:FQ_…`, `path:<src>-><dst>`), so an agent that
cites what the tools returned is gradable without translation.

## Answer (M2 output contract)

```json
{"summary": "… is IDLE [0]. … is dropped [1].",
 "evidence": [{"kind":"nqe","ref":"certified:bgp-peerings-not-established@1","snapshot_id":"712"},
              {"kind":"path_search","ref":"path:10.1.1.10->10.2.2.20","snapshot_id":"712"}],
 "confidence": 0.85, "next_step": "…", "verdict": "cause",
 "tool_calls": 4, "tokens": 1850, "latency_ms": 6100,
 "facts": [{"text": "BGP to 192.0.2.2 is IDLE", "evidence": [0]}]}
```

`facts` is optional. Without it, the grader scans `summary` sentence by
sentence: a sentence that names an IP/prefix, interface, VLAN, AS number,
device-like name or protocol state must carry a `[n]` marker pointing at an
evidence entry. Recommendations and questions are ignored.

## Report

Per case: `verdict_correct`, `evidence_matched/total` with an explanation per
gold item, `accurate` (= verdict correct **and** all gold matched),
`uncited_facts`, `unreproduced_citations` (a citation that does not hold on
re-run — worse than none), and the agent's `tool_calls`, `tokens`,
`latency_ms`. Across cases: accuracy, totals.

## Running

```sh
# CI / offline: replay from a recording
make eval-grade-seed
go run ./cmd/eval-grader -cases netorch/eval/cases -answers out/run-42 -fixture netorch/eval/recordings/sandbox.json

# Live, and record a fixture for later offline grading
FORWARD_API_BASE_URL=… FORWARD_API_KEY=… FORWARD_API_SECRET=… \
  go run ./cmd/eval-grader -cases … -answers … -forward -record recordings/sandbox.json
```

Exit status is 0 only when every case is accurate, so the grader can gate a
skill-pack change. The netorch runner (E.3) drives agents, collects answers
and telemetry, and calls this grader for baseline-vs-candidate reports.
