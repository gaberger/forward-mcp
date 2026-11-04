package usecases

import (
	"context"
	"fmt"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/certified"
	"github.com/forward-mcp/internal/ports"
)

// CertifiedQueries is the application service behind the three M1a tools:
// search_certified_queries, check_answerable and run_certified_query.
type CertifiedQueries struct {
	lib    *entities.CertifiedLibrary
	ranker *certified.Ranker
	runner ports.NQERunner
}

// NewCertifiedQueries loads the library through loader, builds the ranker
// with embed, and runs queries through runner.
func NewCertifiedQueries(ctx context.Context, loader ports.CertifiedLibraryLoader, embed ports.EmbeddingService, runner ports.NQERunner) (*CertifiedQueries, error) {
	lib, err := loader.Load(ctx)
	if err != nil {
		return nil, err
	}
	ranker, err := certified.NewRanker(lib, embed)
	if err != nil {
		return nil, fmt.Errorf("certified library: %w", err)
	}
	return &CertifiedQueries{lib: lib, ranker: ranker, runner: runner}, nil
}

// Library exposes the loaded library (version, entries) for display.
func (c *CertifiedQueries) Library() *entities.CertifiedLibrary { return c.lib }

// Search implements search_certified_queries.
func (c *CertifiedQueries) Search(question string, limit int) ([]certified.Match, error) {
	return c.ranker.Search(question, limit)
}

// CheckAnswerable implements check_answerable.
func (c *CertifiedQueries) CheckAnswerable(question string) (certified.Answerability, error) {
	matches, err := c.ranker.Search(question, 5)
	if err != nil {
		return certified.Answerability{}, err
	}
	return certified.CheckAnswerable(question, matches, c.lib), nil
}

// RunRequest is the input to Run.
type RunRequest struct {
	ID         string
	NetworkID  string
	SnapshotID string
	Params     map[string]any
	Limit      int
}

// RunResult is run_certified_query's structured output. Rows are returned
// verbatim from Forward; Evidence is what an agent must cite.
type RunResult struct {
	QueryID        string            `json:"query_id"`
	Title          string            `json:"title"`
	Version        int               `json:"version"`
	LibraryVersion string            `json:"library_version"`
	Certified      bool              `json:"certified"`
	SnapshotID     string            `json:"snapshot_id"`
	Evidence       entities.Evidence `json:"evidence"`
	RowCount       int               `json:"row_count"`
	TotalNumItems  int64             `json:"total_num_items,omitempty"`
	Rows           []map[string]any  `json:"rows"`
	// Warnings lists output-schema drift; the result is still returned.
	Warnings []string `json:"warnings,omitempty"`
}

// Run implements run_certified_query: bind parameters, run the bound NQE
// query, check the output shape, and wrap the rows in an evidence envelope.
func (c *CertifiedQueries) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	q, ok := c.lib.Lookup(req.ID)
	if !ok {
		return nil, fmt.Errorf("certified query %q not found; use search_certified_queries to find ids", req.ID)
	}
	if req.NetworkID == "" {
		return nil, fmt.Errorf("network_id is required")
	}
	params, err := certified.BindParameters(q, req.Params)
	if err != nil {
		return nil, fmt.Errorf("parameters for %s: %w", q.ID, err)
	}
	nqe := entities.NQEQueryParams{
		NetworkID:  req.NetworkID,
		SnapshotID: req.SnapshotID,
		Parameters: params,
	}
	if q.Source.QueryID != "" {
		nqe.QueryID = q.Source.QueryID
	} else {
		return nil, fmt.Errorf("certified query %s is bound by path (%s); path-bound sources arrive with 1a.2", q.ID, q.Source.Path)
	}
	if req.Limit > 0 {
		nqe.Options = &entities.NQEQueryOptions{MaxResults: req.Limit}
	}
	res, err := c.runner.RunQueryByID(ctx, nqe)
	if err != nil {
		return nil, err
	}
	snapshot := res.SnapshotID
	if snapshot == "" {
		snapshot = req.SnapshotID
	}
	return &RunResult{
		QueryID:        q.ID,
		Title:          q.Title,
		Version:        q.Version,
		LibraryVersion: c.lib.Version,
		Certified:      q.Certified,
		SnapshotID:     snapshot,
		Evidence:       CertifiedEvidence(q, c.lib.Version, snapshot),
		RowCount:       len(res.Items),
		TotalNumItems:  res.TotalNumItems,
		Rows:           res.Items,
		Warnings:       certified.CheckOutput(q, res.Items, 50),
	}, nil
}

// CertifiedEvidence builds the citation for a certified query result.
func CertifiedEvidence(q entities.CertifiedQuery, libVersion, snapshotID string) entities.Evidence {
	return entities.Evidence{
		Kind:           entities.EvidenceKindNQE,
		Ref:            fmt.Sprintf("certified:%s@%d", q.ID, q.Version),
		SnapshotID:     snapshotID,
		LibraryVersion: libVersion,
		Certified:      q.Certified,
	}
}

// AdhocNQEEvidence builds the citation for an uncertified NQE run by id.
func AdhocNQEEvidence(queryID, snapshotID string) entities.Evidence {
	return entities.Evidence{Kind: entities.EvidenceKindNQE, Ref: "nqe:" + queryID, SnapshotID: snapshotID}
}

// PathSearchEvidence builds the citation for a path search.
func PathSearchEvidence(src, dst, snapshotID string) entities.Evidence {
	return entities.Evidence{Kind: entities.EvidenceKindPathSearch, Ref: fmt.Sprintf("path:%s->%s", src, dst), SnapshotID: snapshotID}
}
