// Package redact strips credential-looking substrings from text that is
// about to be logged, traced or returned to a caller (spec non-negotiable
// #4). The patterns are deliberately broad; a false positive costs nothing.
package redact

import (
	"regexp"
	"strings"
)

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

// String replaces anything that looks like a credential with [REDACTED].
func String(s string) string {
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
