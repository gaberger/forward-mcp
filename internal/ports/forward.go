package ports

import (
	"context"

	"github.com/forward-mcp/internal/domain/entities"
)

type ForwardClientInterface interface {
	SendChatRequest(req *entities.ChatRequest) (*entities.ChatResponse, error)
	GetAvailableModels() ([]string, error)

	GetNetworks() ([]entities.Network, error)
	CreateNetwork(name string) (*entities.Network, error)
	DeleteNetwork(networkID string) (*entities.Network, error)
	UpdateNetwork(networkID string, update *entities.NetworkUpdate) (*entities.Network, error)

	SearchPaths(networkID string, params *entities.PathSearchParams) (*entities.PathSearchResponse, error)
	SearchPathsBulk(networkID string, request *entities.PathSearchBulkRequest, snapshotID string) ([]entities.PathSearchBulkResponse, error)

	RunNQEQueryByString(params *entities.NQEQueryParams) (*entities.NQERunResult, error)
	RunNQEQueryByID(params *entities.NQEQueryParams) (*entities.NQERunResult, error)
	GetNQEQueries(dir string) ([]entities.NQEQuery, error)
	GetNQEOrgQueries() ([]entities.NQEQuery, error)
	GetNQEOrgQueriesEnhanced() ([]entities.NQEQueryDetail, error)
	GetNQEOrgQueriesEnhancedWithCache(existingCommitIDs map[string]string) ([]entities.NQEQueryDetail, error)
	GetNQEOrgQueriesEnhancedWithCacheContext(ctx context.Context, existingCommitIDs map[string]string) ([]entities.NQEQueryDetail, error)
	GetNQEFwdQueries() ([]entities.NQEQuery, error)
	GetNQEFwdQueriesEnhanced() ([]entities.NQEQueryDetail, error)
	GetNQEFwdQueriesEnhancedWithCache(existingCommitIDs map[string]string) ([]entities.NQEQueryDetail, error)
	GetNQEFwdQueriesEnhancedWithCacheContext(ctx context.Context, existingCommitIDs map[string]string) ([]entities.NQEQueryDetail, error)
	GetNQEAllQueriesEnhanced() ([]entities.NQEQueryDetail, error)
	GetNQEAllQueriesEnhancedWithCache(existingCommitIDs map[string]string) ([]entities.NQEQueryDetail, error)
	GetNQEAllQueriesEnhancedWithCacheContext(ctx context.Context, existingCommitIDs map[string]string) ([]entities.NQEQueryDetail, error)
	GetNQEQueryByCommit(commitID string, path string, repository string) (*entities.NQEQueryDetail, error)
	GetNQEQueryByCommitWithContext(ctx context.Context, commitID string, path string, repository string) (*entities.NQEQueryDetail, error)
	DiffNQEQuery(before, after string, request *entities.NQEDiffRequest) (*entities.NQEDiffResult, error)

	GetDevices(networkID string, params *entities.DeviceQueryParams) (*entities.DeviceResponse, error)
	GetDeviceLocations(networkID string) (map[string]string, error)
	UpdateDeviceLocations(networkID string, locations map[string]string) error

	GetSnapshots(networkID string) ([]entities.Snapshot, error)
	GetLatestSnapshot(networkID string) (*entities.Snapshot, error)
	DeleteSnapshot(snapshotID string) error

	GetLocations(networkID string) ([]entities.Location, error)
	CreateLocation(networkID string, location *entities.LocationCreate) (*entities.Location, error)
	CreateLocationsBulk(networkID string, locations []entities.LocationBulkPatch) error
	UpdateLocation(networkID string, locationID string, update *entities.LocationUpdate) (*entities.Location, error)
	DeleteLocation(networkID string, locationID string) (*entities.Location, error)
}
