package certified

import (
	"fmt"

	"github.com/forward-mcp/internal/domain/entities"
)

// Recommendation is check_answerable's verdict on how to answer a question.
type Recommendation string

const (
	// RecommendCertified: run the top certified match.
	RecommendCertified Recommendation = "certified"
	// RecommendAdhoc: nothing certified fits well; write ad-hoc NQE (results
	// will be labelled uncertified) or use search_nqe_queries.
	RecommendAdhoc Recommendation = "adhoc"
	// RecommendUnanswerable: no NQE query, certified or otherwise, looks
	// relevant. The agent should ask or say so rather than guess.
	RecommendUnanswerable Recommendation = "unanswerable"
)

// Thresholds on the best match score. Documented in docs/certified-library.md;
// recalibrate against the eval set when the library grows.
const (
	CertifiedThreshold = 0.60
	AdhocThreshold     = 0.30
)

// Answerability is check_answerable's structured answer.
type Answerability struct {
	Question string  `json:"question"`
	Coverage float64 `json:"coverage"`
	// MatchedQueryIDs lists candidates above AdhocThreshold, best first.
	MatchedQueryIDs []string `json:"matched_query_ids"`
	// Missing is what the agent still has to supply or what the library
	// lacks: required parameters of the best match, and a coverage note.
	Missing        []string       `json:"missing"`
	Recommendation Recommendation `json:"recommendation"`
	// Matches carries the scored candidates for display.
	Matches []Match `json:"matches,omitempty"`
}

// CheckAnswerable derives coverage and a recommendation from ranked matches.
// It is pure so the thresholds can be unit-tested without an embedder.
func CheckAnswerable(question string, matches []Match, lib *entities.CertifiedLibrary) Answerability {
	a := Answerability{Question: question, MatchedQueryIDs: []string{}, Missing: []string{}}
	for _, m := range matches {
		if m.Score >= AdhocThreshold {
			a.MatchedQueryIDs = append(a.MatchedQueryIDs, m.ID)
			a.Matches = append(a.Matches, m)
		}
	}
	if len(matches) > 0 {
		a.Coverage = matches[0].Score
	}
	switch {
	case a.Coverage >= CertifiedThreshold && matches[0].Certified:
		a.Recommendation = RecommendCertified
	case a.Coverage >= CertifiedThreshold:
		// Strong match but the entry is not yet certified: usable, labelled.
		a.Recommendation = RecommendAdhoc
		a.Missing = append(a.Missing, fmt.Sprintf("entry %q is not certified yet; results will be labelled uncertified", matches[0].ID))
	case a.Coverage >= AdhocThreshold:
		a.Recommendation = RecommendAdhoc
		a.Missing = append(a.Missing, "no certified query covers this question well; use ad-hoc NQE and cite the snapshot")
	default:
		a.Recommendation = RecommendUnanswerable
		a.Missing = append(a.Missing, "no library query matches this question")
		return a
	}
	if q, ok := lib.Lookup(matches[0].ID); ok {
		for _, p := range q.Parameters {
			if p.Required {
				a.Missing = append(a.Missing, fmt.Sprintf("parameter %q (%s) is required: %s", p.Name, p.Type, p.Description))
			}
		}
	}
	return a
}
