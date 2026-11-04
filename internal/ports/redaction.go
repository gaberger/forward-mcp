package ports

// OutputRedactor scrubs secrets from text before it leaves forward-mcp as a
// tool result (spec non-negotiable #4). Device configuration carries
// passwords, SNMP communities, AAA and routing keys; every tool result is
// passed through the redactor so none of them reach a model, a trace or a
// recording. Implementations must be safe for concurrent use and
// idempotent; they return the redacted text and the replacement count.
type OutputRedactor interface {
	Redact(text string) (string, int)
}
