// Package evalrerun implements ports.EvidenceRerun two ways: in-process
// against Forward (through the certified-query use case, the NQE runner and
// the path-search client) for real grading, and from a recorded fixture
// file for CI and for netorch's runner tests.
package evalrerun

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/forward"
	"github.com/forward-mcp/internal/ports"
	"github.com/forward-mcp/internal/usecases"
)

// Forward re-runs evidence against a live Forward instance.
type Forward struct {
	certified *usecases.CertifiedQueries
	nqe       ports.NQERunner
	paths     forward.ClientInterface
}

var _ ports.EvidenceRerun = (*Forward)(nil)

// NewForward builds the live backend. certified may be nil if only ad-hoc
// refs are expected.
func NewForward(certified *usecases.CertifiedQueries, nqe ports.NQERunner, paths forward.ClientInterface) *Forward {
	return &Forward{certified: certified, nqe: nqe, paths: paths}
}

// RerunQuery implements ports.EvidenceRerun.
func (f *Forward) RerunQuery(ctx context.Context, ref, snapshotID, networkID string, params map[string]any) ([]map[string]any, error) {
	kind, rest, ok := strings.Cut(ref, ":")
	if !ok {
		return nil, fmt.Errorf("malformed evidence ref %q", ref)
	}
	switch kind {
	case "certified":
		if f.certified == nil {
			return nil, fmt.Errorf("certified library not available for ref %q", ref)
		}
		id, _, _ := strings.Cut(rest, "@") // version is informational; the library decides
		res, err := f.certified.Run(ctx, usecases.RunRequest{ID: id, NetworkID: networkID, SnapshotID: snapshotID, Params: params})
		if err != nil {
			return nil, err
		}
		return res.Rows, nil
	case "nqe":
		if strings.HasPrefix(rest, "adhoc:") {
			return nil, fmt.Errorf("ad-hoc NQE ref %q cannot be re-run: source text is not recorded", ref)
		}
		res, err := f.nqe.RunQueryByID(ctx, entities.NQEQueryParams{NetworkID: networkID, SnapshotID: snapshotID, QueryID: rest, Parameters: params})
		if err != nil {
			return nil, err
		}
		return res.Items, nil
	default:
		return nil, fmt.Errorf("unsupported query ref kind %q", kind)
	}
}

// RerunPathSearch implements ports.EvidenceRerun. The ref is
// "path:<src>-><dst>" as emitted by search_paths*; src may be an IP or a
// device name.
func (f *Forward) RerunPathSearch(ctx context.Context, ref, snapshotID, networkID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	src, dst, err := ParsePathRef(ref)
	if err != nil {
		return "", err
	}
	q := forward.PathSearchParams{DstIP: dst, MaxResults: 1, MaxCandidates: 1}
	if net.ParseIP(src) != nil || strings.Contains(src, "/") {
		q.SrcIP = src
	} else {
		q.From = src
	}
	responses, err := f.paths.SearchPathsBulk(networkID, &forward.PathSearchBulkRequest{Queries: []forward.PathSearchParams{q}}, snapshotID)
	if err != nil {
		return "", err
	}
	if len(responses) == 0 {
		return "", fmt.Errorf("path search returned no response")
	}
	paths := responses[0].Info.Paths
	if len(paths) == 0 {
		return "UNREACHABLE", nil
	}
	return paths[0].ForwardingOutcome, nil
}

// ParsePathRef splits "path:<src>-><dst>".
func ParsePathRef(ref string) (src, dst string, err error) {
	rest, ok := strings.CutPrefix(ref, "path:")
	if !ok {
		return "", "", fmt.Errorf("malformed path ref %q", ref)
	}
	src, dst, ok = strings.Cut(rest, "->")
	if !ok || src == "" || dst == "" {
		return "", "", fmt.Errorf("malformed path ref %q", ref)
	}
	return src, dst, nil
}
