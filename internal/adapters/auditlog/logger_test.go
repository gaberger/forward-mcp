package auditlog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/forward-mcp/internal/domain/entities"
)

type captureSink struct{ lines []string }

func (c *captureSink) Info(format string, args ...interface{}) {
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func TestLogProposeCallWritesStructuredLine(t *testing.T) {
	sink := &captureSink{}
	l := New(sink)
	l.now = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

	l.LogProposeCall(context.Background(), entities.AuditEvent{
		Tool:        "predict_run_and_wait",
		Subject:     "agent-troubleshoot",
		Issuer:      "https://issuer.example",
		NetworkID:   "162112",
		ChangeSetID: "cs-1",
		Outcome:     entities.AuditOutcomeOK,
		Duration:    1500 * time.Millisecond,
	})

	if len(sink.lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(sink.lines))
	}
	line := sink.lines[0]
	if !strings.HasPrefix(line, "AUDIT ") {
		t.Fatalf("missing AUDIT prefix: %s", line)
	}
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "AUDIT ")), &rec); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, line)
	}
	for k, want := range map[string]interface{}{
		"kind":          "propose_call",
		"tool":          "predict_run_and_wait",
		"subject":       "agent-troubleshoot",
		"network_id":    "162112",
		"change_set_id": "cs-1",
		"outcome":       "ok",
		"ts":            "2026-11-04T12:00:00Z",
		"duration_ms":   float64(1500),
	} {
		if rec[k] != want {
			t.Errorf("%s: got %v want %v", k, rec[k], want)
		}
	}
}

func TestAuditNeverLeaksSecrets(t *testing.T) {
	sink := &captureSink{}
	l := New(sink)
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhZ2VudCJ9.abcdefghijklmnopqrstuvwxyz0123456789"
	l.LogProposeCall(context.Background(), entities.AuditEvent{
		Tool:    "predict_create_change_set",
		Subject: "agent",
		Outcome: entities.AuditOutcomeError,
		Error: "upstream 401: Authorization: Bearer " + jwt +
			" api_key=AKIA1234567890 FORWARD_API_SECRET=s3cr3tvalue password: hunter22",
	})
	line := sink.lines[0]
	for _, leak := range []string{jwt, "AKIA1234567890", "s3cr3tvalue", "hunter22"} {
		if strings.Contains(line, leak) {
			t.Errorf("audit line leaked %q:\n%s", leak, line)
		}
	}
	if !strings.Contains(line, "[REDACTED]") {
		t.Errorf("expected redaction marker in %s", line)
	}
}

func TestRedactKeepsKeyNames(t *testing.T) {
	got := Redact("token=abc12345 and api_key: xyz")
	if got != "token=[REDACTED] and api_key:[REDACTED]" {
		t.Errorf("unexpected redaction: %q", got)
	}
	if Redact("") != "" {
		t.Error("empty in, empty out")
	}
	if Redact("snapshot 12345 processed") != "snapshot 12345 processed" {
		t.Error("benign text must pass through")
	}
}
