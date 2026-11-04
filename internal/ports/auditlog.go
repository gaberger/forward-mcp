package ports

import (
	"context"

	"github.com/forward-mcp/internal/domain/entities"
)

// AuditLogger records propose-class tool calls. Implementations must
// redact secrets and must not block the caller for long (the logger
// adapter writes to the existing stderr/file logger).
type AuditLogger interface {
	LogProposeCall(ctx context.Context, ev entities.AuditEvent)
}
