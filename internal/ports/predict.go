package ports

import (
	"context"
	"encoding/json"

	"github.com/forward-mcp/internal/domain/entities"
)

// PredictClient is the single interface behind which every Forward Predict
// call is isolated (spec 1b). adapters/predicthttp implements it over
// ForwardHTTP; tests use the same adapter over a fixture transport.
type PredictClient interface {
	CreateChangeSet(ctx context.Context, networkID, name, baseSnapshotID, description string, tags []string) (entities.ChangeSet, error)
	ListChangeSets(ctx context.Context, networkID string) ([]entities.ChangeSet, error)
	DeleteChangeSet(ctx context.Context, networkID, changeSetID string) error

	SetDeviceCommands(ctx context.Context, networkID, changeSetID, device, cli string) error
	ValidateDeviceCommands(ctx context.Context, networkID, changeSetID, device, cli string) (entities.CommandValidation, error)
	AddSecurityRule(ctx context.Context, networkID, changeSetID, device, scopeID, rulebaseID string, definition json.RawMessage) error
	UpdateSecurityRule(ctx context.Context, networkID, changeSetID, device, scopeID, rulebaseID, ruleID string, definition json.RawMessage) error
	DeleteSecurityRule(ctx context.Context, networkID, changeSetID, device, scopeID, rulebaseID, ruleID string) error

	Commit(ctx context.Context, networkID, changeSetID, note string) (commitID string, err error)
	Predict(ctx context.Context, networkID, changeSetID, note string) (entities.PredictedSnapshot, error)
	ListPredictedSnapshots(ctx context.Context, networkID, changeSetID string) ([]entities.PredictedSnapshot, error)
	// SnapshotState looks the snapshot up in the network's snapshot list
	// (there is no single-snapshot endpoint).
	SnapshotState(ctx context.Context, networkID, snapshotID string) (entities.PredictedSnapshot, error)

	SubnetConnectivityDiff(ctx context.Context, beforeSnapshotID, afterSnapshotID string) (entities.Connectivity, error)
	AreaDiffCount(ctx context.Context, beforeSnapshotID, afterSnapshotID, area string) (entities.AreaCount, error)
	RoutingLoopDiffCount(ctx context.Context, beforeSnapshotID, afterSnapshotID string) (entities.AreaCount, error)
	CheckDiffCounts(ctx context.Context, beforeSnapshotID, afterSnapshotID string) (map[string]entities.AreaCount, error)
	VulnerabilityDiffCounts(ctx context.Context, beforeSnapshotID, afterSnapshotID string) (newCVEs *int, err error)
}
