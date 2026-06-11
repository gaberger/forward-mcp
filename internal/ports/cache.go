package ports

import (
	"context"
	"time"

	"github.com/forward-mcp/internal/domain/entities"
)

type CacheInterface interface {
	GetWithContext(ctx context.Context, query, networkID, snapshotID string) (*entities.NQERunResult, bool)
	PutWithContext(ctx context.Context, query, networkID, snapshotID string, result *entities.NQERunResult) error
	Delete(ctx context.Context, query, networkID, snapshotID string) error
	DeleteByNetwork(ctx context.Context, networkID string) (int, error)
	Clear(ctx context.Context) error
	FindSimilarQueriesWithContext(ctx context.Context, query string, limit int) ([]*CacheEntry, error)
	ClearExpiredWithContext(ctx context.Context) (int, error)
	GetStatsWithContext(ctx context.Context) (*CacheStats, error)
	HealthCheck(ctx context.Context) error
	Close() error
}

type CacheStats struct {
	TotalEntries        int64            `json:"total_entries"`
	MaxEntries          int64            `json:"max_entries"`
	TotalQueries        int64            `json:"total_queries"`
	HitCount            int64            `json:"hit_count"`
	MissCount           int64            `json:"miss_count"`
	HitRate             float64          `json:"hit_rate"`
	MemoryUsageBytes    int64            `json:"memory_usage_bytes"`
	MemoryUsageMB       float64          `json:"memory_usage_mb"`
	MaxMemoryMB         float64          `json:"max_memory_mb"`
	AvgResponseTimeMs   float64          `json:"avg_response_time_ms"`
	CompressionRatio    float64          `json:"compression_ratio"`
	EvictedCount        int64            `json:"evicted_count"`
	EvictionsByPolicy   map[string]int64 `json:"evictions_by_policy,omitempty"`
	TTLHours            int              `json:"ttl_hours"`
	SimilarityThreshold float64          `json:"similarity_threshold"`
	EvictionPolicy      string           `json:"eviction_policy"`
	CompressionEnabled  bool             `json:"compression_enabled"`
	LastCleanup         time.Time        `json:"last_cleanup"`
	StartedAt           time.Time        `json:"started_at,omitempty"`
	BackendType         string           `json:"backend_type"`
	RedisConnected      bool             `json:"redis_connected,omitempty"`
	UsingFallback       bool             `json:"using_fallback,omitempty"`
	InstanceID          string           `json:"instance_id,omitempty"`
}

type CacheEntry struct {
	Query           string                 `json:"query"`
	NetworkID       string                 `json:"network_id"`
	SnapshotID      string                 `json:"snapshot_id"`
	Result          *entities.NQERunResult `json:"result"`
	SimilarityScore float64                `json:"similarity_score"`
	CreatedAt       time.Time              `json:"created_at"`
	ExpiresAt       time.Time              `json:"expires_at"`
}

type CacheMode string

const (
	CacheModeMemory CacheMode = "memory"
	CacheModeRedis  CacheMode = "redis"
	CacheModeHybrid CacheMode = "hybrid"
)

func ValidCacheModes() []CacheMode {
	return []CacheMode{CacheModeMemory, CacheModeRedis, CacheModeHybrid}
}

func (m CacheMode) IsValid() bool {
	switch m {
	case CacheModeMemory, CacheModeRedis, CacheModeHybrid:
		return true
	}
	return false
}

func (m CacheMode) String() string {
	return string(m)
}
