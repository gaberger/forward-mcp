// Package predicthttp implements ports.PredictClient over ports.ForwardHTTP
// according to docs/predict-endpoints.md. It is the only place that knows
// Predict's URLs, bodies and status codes. Any observed behaviour that
// contradicts the reference surfaces as a *DivergenceError (spec 1b: stop
// and report, never adapt silently).
package predicthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/domain/services/redact"
	"github.com/forward-mcp/internal/ports"
)

// DivergenceError records where live behaviour departed from the reference.
type DivergenceError struct {
	Operation string // operationId from the reference
	Expected  string
	Observed  string
}

func (e *DivergenceError) Error() string {
	return fmt.Sprintf("DIVERGENCE in %s: expected %s; observed %s", e.Operation, e.Expected, e.Observed)
}

// APIError is a non-2xx response the reference does list (400, 403, 404,
// 409 ...), with Forward's ErrorInfo text when present.
type APIError struct {
	Operation string
	Status    int
	Detail    string
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Operation, e.Status, e.Detail)
	}
	return fmt.Sprintf("%s: HTTP %d", e.Operation, e.Status)
}

// Client talks to Forward Predict through a ForwardHTTP.
type Client struct {
	http ports.ForwardHTTP
}

var _ ports.PredictClient = (*Client)(nil)

// New wraps h.
func New(h ports.ForwardHTTP) *Client { return &Client{http: h} }

const textPlain = "text/plain; charset=utf-8"

// call performs one operation and enforces the reference's expected
// statuses. want lists acceptable success statuses; known lists documented
// error statuses (returned as APIError). Anything else is a divergence.
func (c *Client) call(ctx context.Context, op string, req ports.RawRequest, want []int, known []int, out any) error {
	res, err := c.http.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if contains(want, res.Status) {
		if out == nil || len(res.Body) == 0 {
			return nil
		}
		if err := json.Unmarshal(res.Body, out); err != nil {
			return &DivergenceError{Operation: op, Expected: "JSON body per reference", Observed: fmt.Sprintf("HTTP %d with undecodable body: %s", res.Status, truncate(redact.String(string(res.Body)), 200))}
		}
		return nil
	}
	if contains(known, res.Status) || res.Status == 401 || res.Status == 403 || res.Status == 404 || res.Status == 429 || res.Status >= 500 {
		return &APIError{Operation: op, Status: res.Status, Detail: errorDetail(res.Body)}
	}
	return &DivergenceError{Operation: op, Expected: fmt.Sprintf("HTTP %v", want), Observed: fmt.Sprintf("HTTP %d: %s", res.Status, truncate(redact.String(string(res.Body)), 200))}
}

func errorDetail(body []byte) string {
	var ei struct {
		Message string `json:"message"`
		Error   string `json:"error"`
		Details string `json:"details"`
	}
	if json.Unmarshal(body, &ei) == nil {
		for _, s := range []string{ei.Message, ei.Details, ei.Error} {
			if s != "" {
				return redact.String(s)
			}
		}
	}
	return truncate(redact.String(strings.TrimSpace(string(body))), 200)
}

// decodeListOrWrapped accepts a bare JSON array or an object whose key
// holds the array; anything else is a shape divergence.
func decodeListOrWrapped(raw json.RawMessage, key string, out any) error {
	if err := json.Unmarshal(raw, out); err == nil {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	inner, ok := obj[key]
	if !ok {
		return fmt.Errorf("object without %q", key)
	}
	return json.Unmarshal(inner, out)
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func csPath(networkID, changeSetID string, more ...string) string {
	p := "/api/networks/" + url.PathEscape(networkID) + "/change-sets"
	if changeSetID != "" {
		p += "/" + url.PathEscape(changeSetID)
	}
	for _, m := range more {
		p += "/" + m
	}
	return p
}

func diffPath(before, after, rest string) string {
	return "/api/diffs/" + url.PathEscape(before) + "/" + url.PathEscape(after) + "/" + rest
}

func jsonBody(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// --- change sets ---

type wireChangeSet struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	NetworkID   string   `json:"networkId"`
	SnapshotID  string   `json:"snapshotId"`
	Tags        []string `json:"tags"`
	CreatedAt   string   `json:"createdAt"`
	CreatedBy   string   `json:"createdBy"`
}

func (w wireChangeSet) entity() entities.ChangeSet {
	return entities.ChangeSet{ID: w.ID, NetworkID: w.NetworkID, Name: w.Name, Description: w.Description, BaseSnapshotID: w.SnapshotID, Tags: w.Tags, CreatedAt: w.CreatedAt, CreatedBy: w.CreatedBy}
}

func (c *Client) CreateChangeSet(ctx context.Context, networkID, name, baseSnapshotID, description string, tags []string) (entities.ChangeSet, error) {
	body := map[string]any{"name": name, "snapshotId": baseSnapshotID}
	if description != "" {
		body["description"] = description
	}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	var w wireChangeSet
	err := c.call(ctx, "createChangeSet", ports.RawRequest{Method: "POST", Path: csPath(networkID, ""), ContentType: "application/json", Body: jsonBody(body)}, []int{200, 201}, []int{400}, &w)
	if err != nil {
		return entities.ChangeSet{}, err
	}
	if w.ID == "" {
		return entities.ChangeSet{}, &DivergenceError{Operation: "createChangeSet", Expected: "ChangeSet.id in response", Observed: "no id"}
	}
	cs := w.entity()
	if cs.NetworkID == "" {
		cs.NetworkID = networkID
	}
	return cs, nil
}

func (c *Client) ListChangeSets(ctx context.Context, networkID string) ([]entities.ChangeSet, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "listChangeSets", ports.RawRequest{Method: "GET", Path: csPath(networkID, ""), Idempotent: true}, []int{200}, nil, &raw); err != nil {
		return nil, err
	}
	// The reference shows a bare array; tolerate {"changeSets":[...]} too.
	var list []wireChangeSet
	if err := decodeListOrWrapped(raw, "changeSets", &list); err != nil {
		return nil, &DivergenceError{Operation: "listChangeSets", Expected: "array of ChangeSet (or {changeSets:[...]})", Observed: truncate(string(raw), 200)}
	}
	out := make([]entities.ChangeSet, 0, len(list))
	for _, w := range list {
		out = append(out, w.entity())
	}
	return out, nil
}

func (c *Client) DeleteChangeSet(ctx context.Context, networkID, changeSetID string) error {
	return c.call(ctx, "deleteChangeSet", ports.RawRequest{Method: "DELETE", Path: csPath(networkID, changeSetID), Idempotent: true}, []int{200, 204}, nil, nil)
}

// --- edits ---

func (c *Client) SetDeviceCommands(ctx context.Context, networkID, changeSetID, device, cli string) error {
	return c.call(ctx, "setChangeSetDeviceCommands", ports.RawRequest{
		Method: "PUT", Path: csPath(networkID, changeSetID, "draft", "devices", url.PathEscape(device), "commands"),
		ContentType: textPlain, Body: []byte(cli), Idempotent: true,
	}, []int{200, 204}, []int{400}, nil)
}

func (c *Client) ValidateDeviceCommands(ctx context.Context, networkID, changeSetID, device, cli string) (entities.CommandValidation, error) {
	var res struct {
		CommandErrors []json.RawMessage `json:"commandErrors"`
	}
	req := ports.RawRequest{
		Method: "POST", Path: csPath(networkID, changeSetID, "devices", url.PathEscape(device), "commands"),
		Query: url.Values{"action": {"validate"}}, ContentType: "application/json",
		Body: jsonBody(map[string]any{"commands": cli, "cursorLineNum": 1, "cursorColumnNum": 1}), Idempotent: true,
	}
	if err := c.call(ctx, "validateChangeSetDeviceCommands", req, []int{200}, []int{400}, &res); err != nil {
		return entities.CommandValidation{}, err
	}
	v := entities.CommandValidation{Valid: len(res.CommandErrors) == 0, Advisory: "validation is advisory: PAN-OS returns nothing and EOS misses some valid commands"}
	for _, e := range res.CommandErrors {
		v.Errors = append(v.Errors, truncate(string(e), 300))
	}
	return v, nil
}

func rulePath(networkID, changeSetID, device, scopeID, rulebaseID, ruleID string) string {
	p := csPath(networkID, changeSetID, "devices", url.PathEscape(device), "scopes", url.PathEscape(scopeID), "rulebases", url.PathEscape(rulebaseID), "security-rules")
	if ruleID != "" {
		p += "/" + url.PathEscape(ruleID)
	}
	return p
}

func (c *Client) AddSecurityRule(ctx context.Context, networkID, changeSetID, device, scopeID, rulebaseID string, definition json.RawMessage) error {
	return c.call(ctx, "addSecurityRule", ports.RawRequest{Method: "POST", Path: rulePath(networkID, changeSetID, device, scopeID, rulebaseID, ""), ContentType: "application/json", Body: jsonBody(map[string]any{"definition": definition})}, []int{200, 201, 204}, []int{400, 409}, nil)
}

func (c *Client) UpdateSecurityRule(ctx context.Context, networkID, changeSetID, device, scopeID, rulebaseID, ruleID string, definition json.RawMessage) error {
	return c.call(ctx, "updateSecurityRule", ports.RawRequest{Method: "PATCH", Path: rulePath(networkID, changeSetID, device, scopeID, rulebaseID, ruleID), ContentType: "application/json", Body: jsonBody(map[string]any{"definition": definition}), Idempotent: true}, []int{200, 204}, []int{400, 409}, nil)
}

func (c *Client) DeleteSecurityRule(ctx context.Context, networkID, changeSetID, device, scopeID, rulebaseID, ruleID string) error {
	return c.call(ctx, "deleteSecurityRule", ports.RawRequest{Method: "DELETE", Path: rulePath(networkID, changeSetID, device, scopeID, rulebaseID, ruleID), Idempotent: true}, []int{200, 204}, []int{400, 409}, nil)
}

// --- commit / predict ---

func (c *Client) Commit(ctx context.Context, networkID, changeSetID, note string) (string, error) {
	if strings.TrimSpace(note) == "" {
		return "", errors.New("commitChangeSet: note is required and must not be blank")
	}
	var res struct {
		CommitID string `json:"commitId"`
	}
	err := c.call(ctx, "commitChangeSet", ports.RawRequest{Method: "POST", Path: csPath(networkID, changeSetID, "commits"), Query: url.Values{"note": {note}}}, []int{200, 201}, []int{400, 409}, &res)
	if err != nil {
		return "", err
	}
	if res.CommitID == "" {
		return "", &DivergenceError{Operation: "commitChangeSet", Expected: "ChangeSetCommit.commitId", Observed: "no commitId"}
	}
	return res.CommitID, nil
}

type wireSnapshot struct {
	ID                string `json:"id"`
	Note              string `json:"note"`
	CreatedAt         string `json:"createdAt"`
	ProcessedAt       string `json:"processedAt"`
	ProcessingTrigger string `json:"processingTrigger"`
	State             string `json:"state"`
}

func (w wireSnapshot) entity() entities.PredictedSnapshot {
	return entities.PredictedSnapshot{ID: w.ID, Note: w.Note, CreatedAt: w.CreatedAt, ProcessedAt: w.ProcessedAt, ProcessingTrigger: w.ProcessingTrigger, State: w.State}
}

// Predict starts a prediction. Not idempotent: a retry after an ambiguous
// failure could create a second predicted snapshot, so callers recover via
// ListPredictedSnapshots instead of retrying.
func (c *Client) Predict(ctx context.Context, networkID, changeSetID, note string) (entities.PredictedSnapshot, error) {
	if strings.TrimSpace(note) == "" {
		return entities.PredictedSnapshot{}, errors.New("predictChangeSet: note is required")
	}
	var w wireSnapshot
	err := c.call(ctx, "predictChangeSet", ports.RawRequest{Method: "POST", Path: csPath(networkID, changeSetID), Query: url.Values{"action": {"predict"}, "note": {note}}}, []int{200, 201, 202}, []int{400, 409}, &w)
	if err != nil {
		return entities.PredictedSnapshot{}, err
	}
	if w.ID == "" {
		return entities.PredictedSnapshot{}, &DivergenceError{Operation: "predictChangeSet", Expected: "PredictedSnapshotMeta.id", Observed: "no id"}
	}
	if w.ProcessingTrigger != "" && w.ProcessingTrigger != "PREDICT" {
		return entities.PredictedSnapshot{}, &DivergenceError{Operation: "predictChangeSet", Expected: "processingTrigger PREDICT", Observed: w.ProcessingTrigger}
	}
	return w.entity(), nil
}

func (c *Client) ListPredictedSnapshots(ctx context.Context, networkID, changeSetID string) ([]entities.PredictedSnapshot, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "listPredictedSnapshots", ports.RawRequest{Method: "GET", Path: csPath(networkID, changeSetID, "predicted-snapshots"), Idempotent: true}, []int{200}, nil, &raw); err != nil {
		return nil, err
	}
	var list []wireSnapshot
	if err := decodeListOrWrapped(raw, "snapshots", &list); err != nil {
		return nil, &DivergenceError{Operation: "listPredictedSnapshots", Expected: "array of PredictedSnapshotMeta (or {snapshots:[...]})", Observed: truncate(string(raw), 200)}
	}
	out := make([]entities.PredictedSnapshot, 0, len(list))
	for _, w := range list {
		out = append(out, w.entity())
	}
	return out, nil
}

// SnapshotState uses the published snapshot list (no single-snapshot GET).
func (c *Client) SnapshotState(ctx context.Context, networkID, snapshotID string) (entities.PredictedSnapshot, error) {
	var res struct {
		Snapshots []wireSnapshot `json:"snapshots"`
	}
	if err := c.call(ctx, "listSnapshots", ports.RawRequest{Method: "GET", Path: "/api/networks/" + url.PathEscape(networkID) + "/snapshots", Idempotent: true}, []int{200}, nil, &res); err != nil {
		return entities.PredictedSnapshot{}, err
	}
	for _, s := range res.Snapshots {
		if s.ID == snapshotID {
			return s.entity(), nil
		}
	}
	return entities.PredictedSnapshot{}, &APIError{Operation: "listSnapshots", Status: 404, Detail: "snapshot " + snapshotID + " not in network " + networkID}
}

// --- results ---

func (c *Client) SubnetConnectivityDiff(ctx context.Context, before, after string) (entities.Connectivity, error) {
	var w struct {
		NewlyConnectedSubnetPairs      int  `json:"newlyConnectedSubnetPairs"`
		NewlyIsolatedSubnetPairs       int  `json:"newlyIsolatedSubnetPairs"`
		NewlyIsolatedImpactedLocations int  `json:"newlyIsolatedImpactedLocations"`
		ModifiedSubnetPairs            int  `json:"modifiedSubnetPairs"`
		EvaluatedSubnetPairs           int  `json:"evaluatedSubnetPairs"`
		TotalSubnetPairs               int  `json:"totalSubnetPairs"`
		TotalImpactedLocations         int  `json:"totalImpactedLocations"`
		IsPartialResult                bool `json:"isPartialResult"`
	}
	if err := c.call(ctx, "getSubnetConnectivityDiff", ports.RawRequest{Method: "GET", Path: diffPath(before, after, "subnet-connectivity"), Idempotent: true}, []int{200}, []int{400}, &w); err != nil {
		return entities.Connectivity{}, err
	}
	return entities.Connectivity{
		NewlyConnectedSubnetPairs: w.NewlyConnectedSubnetPairs, NewlyIsolatedSubnetPairs: w.NewlyIsolatedSubnetPairs,
		NewlyIsolatedLocations: w.NewlyIsolatedImpactedLocations, ModifiedSubnetPairs: w.ModifiedSubnetPairs,
		EvaluatedSubnetPairs: w.EvaluatedSubnetPairs, TotalSubnetPairs: w.TotalSubnetPairs,
		TotalImpactedLocations: w.TotalImpactedLocations, IsPartialResult: w.IsPartialResult,
	}, nil
}

func (c *Client) AreaDiffCount(ctx context.Context, before, after, area string) (entities.AreaCount, error) {
	var w entities.AreaCount
	err := c.call(ctx, "get"+capitalize(area)+"DiffCount", ports.RawRequest{Method: "GET", Path: diffPath(before, after, area), RawQuery: "count", Idempotent: true}, []int{200}, []int{400}, &w)
	return w, err
}

func (c *Client) RoutingLoopDiffCount(ctx context.Context, before, after string) (entities.AreaCount, error) {
	var w entities.AreaCount
	err := c.call(ctx, "getRoutingLoopDiffCount", ports.RawRequest{Method: "GET", Path: diffPath(before, after, "routing-loop/count"), Idempotent: true}, []int{200}, []int{400}, &w)
	return w, err
}

func (c *Client) CheckDiffCounts(ctx context.Context, before, after string) (map[string]entities.AreaCount, error) {
	var w map[string]entities.AreaCount
	err := c.call(ctx, "getCheckDiffCounts", ports.RawRequest{Method: "GET", Path: diffPath(before, after, "checks"), RawQuery: "counts", Idempotent: true}, []int{200}, []int{400}, &w)
	return w, err
}

func (c *Client) VulnerabilityDiffCounts(ctx context.Context, before, after string) (*int, error) {
	var w struct {
		NewCveCount *int `json:"newCveCount"`
	}
	err := c.call(ctx, "getVulnerabilityDiffCounts", ports.RawRequest{Method: "GET", Path: diffPath(before, after, "vulnerabilities/counts"), Idempotent: true}, []int{200}, []int{400}, &w)
	if err != nil {
		var api *APIError
		if errors.As(err, &api) && (api.Status == 403 || api.Status == 404) {
			return nil, nil // unlicensed deployments: not a failure
		}
		return nil, err
	}
	return w.NewCveCount, nil
}
