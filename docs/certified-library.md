# Certified NQE library

The certified library is a small, reviewed, versioned set of NQE queries that
agents prefer over the 6,000-entry Forward library. Each entry binds a plain
question to one Forward query, declares its parameters and output shape, names
an owner, and ships a recorded fixture. Results carry an evidence envelope the
agent must cite.

Location: `spec/certified/` (override with `FORWARD_CERTIFIED_LIBRARY_PATH`).

## Tools

| Tool | Class | What it does |
|---|---|---|
| `search_certified_queries(question, limit)` | read | Ranked matches `{id, title, score, why, certified}` |
| `check_answerable(question)` | read | `{coverage, matched_query_ids, missing, recommendation}` |
| `run_certified_query(id, network_id, snapshot_id, parameters, limit)` | read | Validates parameters, runs the bound query, checks output shape, returns rows + `evidence` |

Ad-hoc tools keep working: `run_nqe_query_by_id` and `search_paths*` now end
with an `Evidence: {...}` line with `"certified": false` (NQE) or the path
search descriptor, plus the snapshot id.

## Evidence envelope

```json
{"kind":"nqe","ref":"certified:device-basic-info@1","snapshot_id":"712","library_version":"0.1.0","certified":true}
{"kind":"nqe","ref":"nqe:FQ_ac65…","snapshot_id":"712","certified":false}
{"kind":"path_search","ref":"path:10.1.1.10->10.2.2.20","snapshot_id":"712","certified":false}
```

Agents must quote `ref` and `snapshot_id` for every network fact (spec
non-negotiable #3). The eval grader re-runs `ref` against `snapshot_id` and
compares.

## Ranking and answerability

Score = cosine similarity between the question and `title + intent + synonyms`
(using the server's configured embedding provider, keyword TF-IDF by default)
plus 0.15 per title/synonym phrase found verbatim in the question, capped at 1.

| Best score | Recommendation |
|---|---|
| ≥ 0.60 and entry certified | `certified` — run it |
| ≥ 0.60 but entry uncertified | `adhoc` — run it, results are labelled uncertified |
| ≥ 0.30 | `adhoc` — write NQE or use `search_nqe_queries` |
| < 0.30 | `unanswerable` — ask, don't guess |

`missing` lists the top match's required parameters (the agent must supply
them) and any coverage caveat. Thresholds live in
`internal/domain/services/certified/answerable.go`; recalibrate against the
eval set when the library grows.

## Adding an entry

1. Find the Forward query id (`FQ_…`) with `search_nqe_queries` or
   `list_nqe_queries`, run it once and save the result as
   `fixtures/<id>.v1.json` (`{"snapshotId": "...", "items": [...]}`; trim rows,
   keep every column).
2. Write `queries/<id>.json` — see `spec/certified/README.md` for the fields.
   `output_schema` describes **one row**: `type: object`, `required`, and
   `properties` with JSON types. Keep `certified: false` until reviewed.
3. Run `go test ./internal/adapters/certifiedfs/ ./internal/usecases/` — the
   loader rejects anything malformed with the file and field named.
4. Review: the owner confirms the intent, synonyms and schema, then flips
   `certified: true` and bumps `VERSION` (minor for a new entry, major for a
   removed or renamed id).

## Versioning

`VERSION` is the library version reported in every certified result. Entry
`version` is bumped whenever the bound query, parameters or output schema
change, and the fixture is re-recorded. The pair `<id>@<version>` in an
evidence ref is therefore stable for replay.

## Current limits (M1a.1–1a.5)

- Entries bound by `nqe_path` load but cannot run yet; use `nqe_query_id`.
- Output checking is a shape check (required keys, simple types), not full
  JSON Schema validation; drift is returned as `warnings`.
- Search is in-process over the loaded library; there is no precomputed
  embedding cache yet (the library is small).
