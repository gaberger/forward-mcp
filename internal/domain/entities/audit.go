package entities

import "time"

// AuditEvent is the record written for every call to a propose-class tool
// (spec non-negotiable #2). It must never contain credentials, tokens or
// raw request bodies; only identifiers.
type AuditEvent struct {
	Time        time.Time
	Tool        string
	Subject     string
	Issuer      string
	NetworkID   string
	ChangeSetID string
	SnapshotID  string
	// Outcome is "ok", "error" or "denied".
	Outcome string
	// Error is the sanitised error text when Outcome != "ok".
	Error string
	// Duration of the handler call.
	Duration time.Duration
}

const (
	AuditOutcomeOK     = "ok"
	AuditOutcomeError  = "error"
	AuditOutcomeDenied = "denied"
)
