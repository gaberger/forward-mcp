package logger

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// TestLoggerRedactsSecrets: nothing credential-shaped reaches a log line
// through any level (spec non-negotiable #4).
func TestLoggerRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	l := &Logger{
		infoLogger:  log.New(&buf, "", 0),
		debugLogger: log.New(&buf, "", 0),
		debugMode:   true,
	}
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhZ2VudCJ9.abcdefghijklmnopqrstuvwxyz0123456789"
	l.Info("auth header: Bearer %s", jwt)
	l.Warn("config: FORWARD_API_SECRET=supersecretvalue1")
	l.Error("upstream said api_key: AKIAABCDEFGHIJKL")
	l.Debug("password=hunter2hunter2")
	out := buf.String()
	for _, leak := range []string{jwt, "supersecretvalue1", "AKIAABCDEFGHIJKL", "hunter2hunter2"} {
		if strings.Contains(out, leak) {
			t.Errorf("log leaked %q:\n%s", leak, out)
		}
	}
	if strings.Count(out, "[REDACTED]") < 4 {
		t.Errorf("expected four redactions:\n%s", out)
	}
	if !strings.Contains(out, "config: FORWARD_API_SECRET=[REDACTED]") {
		t.Errorf("key name should survive redaction:\n%s", out)
	}
}
