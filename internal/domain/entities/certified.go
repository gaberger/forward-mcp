package entities

import "encoding/json"

// CertifiedQuery is one entry of the certified NQE library (PLAN.md M1a).
// It binds a human intent to a specific Forward NQE query, with the
// parameters it takes, the shape it returns, who vouches for it, and a
// recorded fixture so the entry can be tested without a live instance.
type CertifiedQuery struct {
	// ID is a stable slug, e.g. "device-basic-info". Lower-case, dashes.
	ID string `json:"id"`
	// Version of this entry (bumped when source, params or schema change).
	Version int    `json:"version"`
	Title   string `json:"title"`
	// Intent is the question this query answers, in one or two sentences.
	Intent string `json:"intent"`
	// Synonyms are alternative phrasings used by search and answerability.
	Synonyms   []string         `json:"synonyms,omitempty"`
	Parameters []QueryParameter `json:"parameters,omitempty"`
	// OutputSchema is a JSON Schema for one result row. Kept raw so the
	// domain does not depend on a validator implementation.
	OutputSchema json.RawMessage `json:"output_schema"`
	Owner        string          `json:"owner"`
	// Certified is true only when owner, fixture and schema are all present
	// and the entry has been reviewed. Uncertified entries may be searched
	// but results are labelled uncertified.
	Certified bool        `json:"certified"`
	Source    QuerySource `json:"source"`
	// Fixture is the path, relative to the library root, of a recorded
	// NQERunResult used by tests and by the eval grader.
	Fixture string `json:"fixture"`
}

// QuerySource identifies the Forward NQE query behind an entry. Exactly one
// of QueryID or Path must be set; QueryID is preferred because paths can be
// renamed.
type QuerySource struct {
	// QueryID is the Forward global query id (FQ_...).
	QueryID string `json:"nqe_query_id,omitempty"`
	// Path is the NQE repository path, e.g. "/Devices/Device Basic Info".
	Path string `json:"nqe_path,omitempty"`
	// Repository is "org" or "fwd" (Forward-supplied library). Default "fwd".
	Repository string `json:"repository,omitempty"`
}

// QueryParameter describes one parameter an entry accepts.
type QueryParameter struct {
	Name        string        `json:"name"`
	Type        ParameterType `json:"type"`
	Required    bool          `json:"required"`
	Description string        `json:"description,omitempty"`
	// Default, when set, is used if the parameter is omitted.
	Default json.RawMessage `json:"default,omitempty"`
}

// ParameterType is the small closed set of parameter types certified
// queries accept. Keeping it closed lets check_answerable reason about
// what a question is missing ("which device?").
type ParameterType string

const (
	ParamString ParameterType = "string"
	ParamInt    ParameterType = "int"
	ParamBool   ParameterType = "bool"
	ParamIP     ParameterType = "ip"
	ParamCIDR   ParameterType = "cidr"
	ParamDevice ParameterType = "device"
	ParamList   ParameterType = "list"
)

func (t ParameterType) IsValid() bool {
	switch t {
	case ParamString, ParamInt, ParamBool, ParamIP, ParamCIDR, ParamDevice, ParamList:
		return true
	}
	return false
}

// CertifiedLibrary is a loaded, validated library at one version.
type CertifiedLibrary struct {
	// Version is the semver string from the library's VERSION file.
	Version string
	Queries []CertifiedQuery
}

// Lookup returns the entry with the given ID.
func (l *CertifiedLibrary) Lookup(id string) (CertifiedQuery, bool) {
	for _, q := range l.Queries {
		if q.ID == id {
			return q, true
		}
	}
	return CertifiedQuery{}, false
}

// Evidence is the citation every agent answer must carry for each network
// fact (spec non-negotiable #3). It is attached to every query and path
// search result forward-mcp returns.
type Evidence struct {
	// Kind is "nqe" or "path_search".
	Kind string `json:"kind"`
	// Ref identifies what was run: a certified id ("certified:<id>@<version>"),
	// an NQE query id ("nqe:FQ_..."), ad-hoc source ("nqe:adhoc:<sha256>")
	// or a path search descriptor ("path:<src>-><dst>").
	Ref        string `json:"ref"`
	SnapshotID string `json:"snapshot_id"`
	// LibraryVersion is set only for certified queries.
	LibraryVersion string `json:"library_version,omitempty"`
	Certified      bool   `json:"certified"`
}

const (
	EvidenceKindNQE        = "nqe"
	EvidenceKindPathSearch = "path_search"
)
