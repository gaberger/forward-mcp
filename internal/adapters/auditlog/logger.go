// Package auditlog adapts ports.AuditLogger onto a line-oriented sink. The
// default sink is the server's existing logger (stderr / JSON file), so no
// new dependency or output channel is introduced.
package auditlog

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/forward-mcp/internal/domain/entities"
	"github.com/forward-mcp/internal/ports"
)

// Sink is the minimal interface the adapter needs; *logger.Logger satisfies
// it via Info(format, args...).
type Sink interface {
	Info(format string, args ...interface{})
}

// Logger writes one JSON object per propose call, prefixed with "AUDIT ".
type Logger struct {
	sink Sink
	now  func() time.Time
}

var _ ports.AuditLogger = (*Logger)(nil)

// New returns an audit logger writing to sink.
func New(sink Sink) *Logger {
	return &Logger{sink: sink, now: time.Now}
}

// record is the on-the-wire shape. Field names are stable: the S2 spike and
// the eval report parse them.
type record struct {
	TS          string `json:"ts"`
	Kind        string `json:"kind"`
	Tool        string `json:"tool"`
	Subject     string `json:"subject"`
	Issuer      string `json:"issuer,omitempty"`
	NetworkID   string `json:"network_id,omitempty"`
	ChangeSetID string `json:"change_set_id,omitempty"`
	SnapshotID  string `json:"snapshot_id,omitempty"`
	Outcome     string `json:"outcome"`
	Error       string `json:"error,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
}

// LogProposeCall implements ports.AuditLogger.
func (l *Logger) LogProposeCall(_ context.Context, ev entities.AuditEvent) {
	ts := ev.Time
	if ts.IsZero() {
		ts = l.now()
	}
	r := record{
		TS:          ts.UTC().Format(time.RFC3339Nano),
		Kind:        "propose_call",
		Tool:        ev.Tool,
		Subject:     ev.Subject,
		Issuer:      ev.Issuer,
		NetworkID:   ev.NetworkID,
		ChangeSetID: ev.ChangeSetID,
		SnapshotID:  ev.SnapshotID,
		Outcome:     ev.Outcome,
		Error:       Redact(ev.Error),
		DurationMS:  ev.Duration.Milliseconds(),
	}
	b, err := json.Marshal(r)
	if err != nil {
		l.sink.Info("AUDIT {\"kind\":\"propose_call\",\"tool\":%q,\"outcome\":\"error\",\"error\":\"marshal failed\"}", ev.Tool)
		return
	}
	l.sink.Info("AUDIT %s", string(b))
}

// Secret-looking substrings that must never reach a log line. The patterns
// are deliberately broad; false positives cost nothing.
var secretPatterns = []*regexp.Regexp{
	// JWTs: three base64url segments separated by dots.
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`),
	// Bearer / Basic authorization values.
	regexp.MustCompile(`(?i)(bearer|basic)\s+[A-Za-z0-9+/=_\-.]{8,}`),
	// key=value style secrets.
	regexp.MustCompile(`(?i)(api[_-]?key|api[_-]?secret|secret|token|password|passwd|authorization)\s*[=:]\s*[^\s,;"']+`),
}

// Redact replaces anything that looks like a credential with [REDACTED].
// Exported so the remote transport's error path (M1c.6) can reuse it.
func Redact(s string) string {
	if s == "" {
		return s
	}
	out := s
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			// Keep the key name for key=value matches so logs stay readable.
			if i := strings.IndexAny(m, "=:"); i > 0 && !strings.HasPrefix(strings.ToLower(m), "bearer") && !strings.HasPrefix(strings.ToLower(m), "basic") && !strings.HasPrefix(m, "eyJ") {
				return m[:i+1] + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return out
}
