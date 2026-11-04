package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/forward-mcp/internal/adapters/certifiedfs"
	"github.com/forward-mcp/internal/adapters/forwardclient"
	mcpadapter "github.com/forward-mcp/internal/adapters/mcp"
	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/usecases"
)

// This file exposes the certified NQE library (PLAN.md M1a) as MCP tools.
// All logic lives in usecases.CertifiedQueries; the handlers here only
// parse arguments, apply session defaults and render JSON.

// initCertified loads the certified library from dir and builds the use
// case. A failure is recorded, not fatal: the tools stay registered and
// explain the problem when called, so a misconfigured path is visible to
// the operator and the agent alike.
func (s *ForwardMCPService) initCertified(dir string) {
	if dir == "" {
		dir = "spec/certified"
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		s.certifiedErr = fmt.Errorf("certified library directory %q not found (set FORWARD_CERTIFIED_LIBRARY_PATH)", dir)
		s.logger.Warn("%v", s.certifiedErr)
		return
	}
	uc, err := usecases.NewCertifiedQueries(s.ctx, certifiedfs.New(os.DirFS(dir)), s.embeddingService, forwardclient.NewNQERunner(s.forwardClient))
	if err != nil {
		s.certifiedErr = err
		s.logger.Error("certified library failed to load: %v", err)
		return
	}
	s.certified = uc
	s.logger.Info("Certified NQE library %s loaded from %s (%d entries)", uc.Library().Version, dir, len(uc.Library().Queries))
}

func (s *ForwardMCPService) certifiedReady() (*usecases.CertifiedQueries, error) {
	if s.certified == nil {
		if s.certifiedErr != nil {
			return nil, fmt.Errorf("certified library unavailable: %w", s.certifiedErr)
		}
		return nil, fmt.Errorf("certified library unavailable: not initialised")
	}
	return s.certified, nil
}

// SearchCertifiedQueriesArgs are the arguments for search_certified_queries.
type SearchCertifiedQueriesArgs struct {
	Question string `json:"question" jsonschema:"The network question in plain language, e.g. 'which BGP neighbors are down on edge-1'"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum matches to return (default 5)"`
}

// CheckAnswerableArgs are the arguments for check_answerable.
type CheckAnswerableArgs struct {
	Question string `json:"question" jsonschema:"The network question in plain language"`
}

// RunCertifiedQueryArgs are the arguments for run_certified_query.
type RunCertifiedQueryArgs struct {
	ID         string         `json:"id" jsonschema:"Certified query id from search_certified_queries (e.g. 'device-basic-info')"`
	NetworkID  string         `json:"network_id,omitempty" jsonschema:"Network ID (optional if a default network is set)"`
	SnapshotID string         `json:"snapshot_id,omitempty" jsonschema:"Snapshot ID; omitted means the latest processed snapshot. The snapshot actually used is returned in evidence.snapshot_id"`
	Parameters map[string]any `json:"parameters,omitempty" jsonschema:"Parameters declared by the entry; unknown names are rejected"`
	Limit      int            `json:"limit,omitempty" jsonschema:"Maximum rows to return (default 1000)"`
}

func (s *ForwardMCPService) searchCertifiedQueries(_ context.Context, args SearchCertifiedQueriesArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("search_certified_queries", args, nil)
	uc, err := s.certifiedReady()
	if err != nil {
		return nil, err
	}
	matches, err := uc.Search(args.Question, args.Limit)
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(map[string]any{
		"library_version": uc.Library().Version,
		"question":        args.Question,
		"matches":         matches,
		"hint":            "Run the best match with run_certified_query; results carry an evidence envelope to cite.",
	})
}

func (s *ForwardMCPService) checkAnswerable(_ context.Context, args CheckAnswerableArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("check_answerable", args, nil)
	uc, err := s.certifiedReady()
	if err != nil {
		return nil, err
	}
	a, err := uc.CheckAnswerable(args.Question)
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(map[string]any{
		"library_version": uc.Library().Version,
		"answerability":   a,
	})
}

func (s *ForwardMCPService) runCertifiedQuery(ctx context.Context, args RunCertifiedQueryArgs) (*mcp.CallToolResult, error) {
	s.logToolCall("run_certified_query", args, nil)
	uc, err := s.certifiedReady()
	if err != nil {
		return nil, err
	}
	networkID := s.getNetworkID(args.NetworkID)
	if err := s.validateNetworkID(networkID); err != nil {
		return nil, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 1000
	}
	res, err := uc.Run(ctx, usecases.RunRequest{
		ID:         args.ID,
		NetworkID:  networkID,
		SnapshotID: s.getSnapshotID(args.SnapshotID),
		Params:     args.Parameters,
		Limit:      limit,
	})
	if err != nil {
		return nil, err
	}
	return jsonToolResponse(res)
}

// registerCertifiedTools adds the three M1a tools. They are read-class.
func (s *ForwardMCPService) registerCertifiedTools(r *mcpadapter.Registrar) {
	mcpadapter.AddTool(r, "search_certified_queries",
		"🏅 **CERTIFIED QUERY SEARCH**: Find reviewed, versioned NQE queries that answer a network question.\n\n"+
			"Prefer this over search_nqe_queries: certified entries have a known output schema, a fixture and an owner, "+
			"and their results come with an evidence envelope (query id, library version, snapshot id) you must cite. "+
			"Returns ranked matches with a score and the phrases that matched.",
		s.searchCertifiedQueries)

	mcpadapter.AddTool(r, "check_answerable",
		"🧭 **ANSWERABILITY CHECK**: Before answering a network question, learn whether the certified library covers it.\n\n"+
			"Returns coverage (0..1), matched certified query ids, what is still missing (required parameters, or "+
			"that no certified query fits), and a recommendation: 'certified' (run it), 'adhoc' (write NQE; results "+
			"will be labelled uncertified) or 'unanswerable' (ask instead of guessing).",
		s.checkAnswerable)

	mcpadapter.AddTool(r, "run_certified_query",
		"🏅 **RUN CERTIFIED QUERY**: Execute a certified library query by id against a network snapshot.\n\n"+
			"Parameters are validated against the entry's declaration; rows are checked against the entry's output "+
			"schema (drift is reported as warnings). The response includes 'evidence' — cite its ref and snapshot_id "+
			"for every fact you state from the rows. 'certified: false' means the entry is not yet reviewed.",
		s.runCertifiedQuery)
}

// jsonToolResponse renders v as indented JSON text.
func jsonToolResponse(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	return newToolResponse(newTextContent(string(b))), nil
}

// evidenceLine renders an evidence envelope as the trailing line of a text
// response, so existing prose responses gain a citation without changing
// their shape (PLAN.md 1a.6/1a.7).
func evidenceLine(e entities.Evidence) string {
	b, _ := json.Marshal(e)
	return "\nEvidence: " + string(b) + "\n"
}
