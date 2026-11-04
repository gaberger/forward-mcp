# Certified NQE library

One entry per file under `queries/`, named `<id>.json`; recorded results under
`fixtures/`. `VERSION` is the library's semver and is reported in every
certified result's evidence envelope.

Rules (enforced by `internal/adapters/certifiedfs` at load time):

- `id` is a lower-case dashed slug and equals the file name.
- `source` names exactly one of `nqe_query_id` (`FQ_<40 hex>`, preferred) or `nqe_path`.
- `output_schema` is a JSON Schema for **one result row** (`"type": "object"`).
- `fixture` must exist and parse as an NQE run result (`snapshotId`, `items`).
- `certified: true` requires owner, fixture and output schema, and a review by the owner.
  Until then set `certified: false`; the entry is still searchable and runnable, and its
  results are labelled `"certified": false`.
- Bump `version` whenever source, parameters or output schema change; bump `VERSION`
  (minor for additions, major for removed/renamed ids).

Full guide: `docs/certified-library.md`.
