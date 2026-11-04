// Package forwardhttpfixture is a ports.ForwardHTTP that replays recorded
// exchanges, and a recorder that captures them from a live ForwardHTTP.
// Exchanges are matched by method, path and query, in recorded order per
// key, so a scenario that calls the same endpoint twice (e.g. polling a
// snapshot until PROCESSED) replays each response in turn.
package forwardhttpfixture

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/forward-mcp/internal/domain/services/redact"
	"github.com/forward-mcp/internal/ports"
)

// Exchange is one recorded request/response pair.
type Exchange struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Query       string `json:"query,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// RequestBody is kept for review; it is not used for matching.
	RequestBody string `json:"request_body,omitempty"`
	Status      int    `json:"status"`
	Body        string `json:"body,omitempty"`
}

// File is the on-disk recording.
type File struct {
	// Source notes where the recording came from (host redacted) and when.
	Source    string     `json:"source,omitempty"`
	Exchanges []Exchange `json:"exchanges"`
}

func key(method, path, query string) string {
	return method + " " + path + "?" + query
}

func queryOf(r ports.RawRequest) string {
	parts := make([]string, 0, 2)
	if r.RawQuery != "" {
		parts = append(parts, r.RawQuery)
	}
	if len(r.Query) > 0 {
		parts = append(parts, r.Query.Encode())
	}
	return strings.Join(parts, "&")
}

// Replayer implements ports.ForwardHTTP from a File.
type Replayer struct {
	mu     sync.Mutex
	byKey  map[string][]Exchange
	served map[string]int
	// Unmatched lists requests the recording had nothing for.
	Unmatched []string
}

var _ ports.ForwardHTTP = (*Replayer)(nil)

// Load reads a recording.
func Load(path string) (*Replayer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return NewReplayer(f), nil
}

// NewReplayer builds a replayer from a File.
func NewReplayer(f File) *Replayer {
	r := &Replayer{byKey: map[string][]Exchange{}, served: map[string]int{}}
	for _, e := range f.Exchanges {
		k := key(e.Method, e.Path, e.Query)
		r.byKey[k] = append(r.byKey[k], e)
	}
	return r
}

// Do implements ports.ForwardHTTP.
func (r *Replayer) Do(_ context.Context, req ports.RawRequest) (ports.RawResponse, error) {
	k := key(req.Method, req.Path, queryOf(req))
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.byKey[k]
	if len(list) == 0 {
		r.Unmatched = append(r.Unmatched, k)
		return ports.RawResponse{}, fmt.Errorf("fixture has no recording for %s", k)
	}
	i := r.served[k]
	if i >= len(list) {
		i = len(list) - 1 // repeat the last response (steady state)
	}
	r.served[k] = i + 1
	e := list[i]
	return ports.RawResponse{Status: e.Status, ContentType: "application/json", Body: []byte(e.Body)}, nil
}

// Recorder wraps a live ForwardHTTP and captures exchanges.
type Recorder struct {
	live ports.ForwardHTTP
	mu   sync.Mutex
	file File
}

var _ ports.ForwardHTTP = (*Recorder)(nil)

// NewRecorder wraps live; source is recorded (redacted) in the file.
func NewRecorder(live ports.ForwardHTTP, source string) *Recorder {
	return &Recorder{live: live, file: File{Source: redactHost(source)}}
}

var idRe = regexp.MustCompile(`https?://[^/\s]+`)

func redactHost(s string) string {
	return idRe.ReplaceAllString(redact.String(s), "https://[forward-host]")
}

// Do implements ports.ForwardHTTP.
func (r *Recorder) Do(ctx context.Context, req ports.RawRequest) (ports.RawResponse, error) {
	res, err := r.live.Do(ctx, req)
	if err != nil {
		return res, err
	}
	r.mu.Lock()
	r.file.Exchanges = append(r.file.Exchanges, Exchange{
		Method: req.Method, Path: req.Path, Query: queryOf(req), ContentType: req.ContentType,
		RequestBody: redact.String(string(req.Body)), Status: res.Status, Body: redact.String(string(res.Body)),
	})
	r.mu.Unlock()
	return res, nil
}

// Save writes the recording, exchanges in call order.
func (r *Recorder) Save(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := json.MarshalIndent(r.file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Keys lists the distinct recorded keys, for test diagnostics.
func (r *Replayer) Keys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.byKey))
	for k := range r.byKey {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// MustQuery is a helper for tests building expectations.
func MustQuery(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}
