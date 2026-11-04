package certified

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// Match is one ranked search hit.
type Match struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
	// Why explains the score in one line (which synonyms hit, etc.).
	Why       string `json:"why"`
	Certified bool   `json:"certified"`
}

// Ranker scores a question against every library entry. Entry vectors are
// computed once at construction; the question is embedded per call.
//
// score = cosine(question, title+intent+synonyms) + synonym boost, where the
// boost is 0.15 per distinct synonym (or title) phrase found verbatim in the
// question, capped so the total never exceeds 1. The verbatim boost makes
// the keyword embedder usable for exact operator phrasing while still
// letting an embedding provider handle paraphrases.
type Ranker struct {
	embed   ports.EmbeddingService
	entries []rankedEntry
}

type rankedEntry struct {
	q       entities.CertifiedQuery
	vec     []float64
	phrases []string // lower-cased title + synonyms
}

const (
	synonymBoost = 0.15
	maxScore     = 1.0
)

// NewRanker embeds every entry. It fails if the embedder fails on any entry
// so a broken provider is noticed at startup, not on first query.
func NewRanker(lib *entities.CertifiedLibrary, embed ports.EmbeddingService) (*Ranker, error) {
	r := &Ranker{embed: embed}
	for _, q := range lib.Queries {
		text := EntryText(q)
		vec, err := embed.GenerateEmbedding(text)
		if err != nil {
			return nil, fmt.Errorf("embed %s: %w", q.ID, err)
		}
		phrases := []string{strings.ToLower(q.Title)}
		for _, s := range q.Synonyms {
			phrases = append(phrases, strings.ToLower(s))
		}
		r.entries = append(r.entries, rankedEntry{q: q, vec: vec, phrases: phrases})
	}
	return r, nil
}

// EntryText is the text an entry is embedded from. Exported so the eval
// tooling and a future precomputed-embedding cache agree with the ranker.
func EntryText(q entities.CertifiedQuery) string {
	parts := []string{q.Title, q.Intent}
	parts = append(parts, q.Synonyms...)
	return strings.Join(parts, ". ")
}

// Search returns up to limit matches with score > 0, best first.
func (r *Ranker) Search(question string, limit int) ([]Match, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("question is required")
	}
	if limit <= 0 {
		limit = 5
	}
	qvec, err := r.embed.GenerateEmbedding(question)
	if err != nil {
		return nil, fmt.Errorf("embed question: %w", err)
	}
	lq := strings.ToLower(question)
	var out []Match
	for _, e := range r.entries {
		score := cosine(qvec, e.vec)
		var hits []string
		for _, p := range e.phrases {
			if p != "" && strings.Contains(lq, p) {
				hits = append(hits, p)
			}
		}
		score += synonymBoost * float64(len(hits))
		if score > maxScore {
			score = maxScore
		}
		if score <= 0 {
			continue
		}
		why := fmt.Sprintf("similarity %.2f", cosine(qvec, e.vec))
		if len(hits) > 0 {
			why += "; matched phrases: " + strings.Join(hits, ", ")
		}
		out = append(out, Match{ID: e.q.ID, Title: e.q.Title, Score: round3(score), Why: why, Certified: e.q.Certified})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	c := dot / (math.Sqrt(na) * math.Sqrt(nb))
	if c < 0 {
		return 0
	}
	return c
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
