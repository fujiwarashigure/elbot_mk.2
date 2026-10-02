package health

import "elbot/internal/redact"

// RedactSecrets keeps the health package's historical entry point and delegates
// to the shared redaction implementation used by user errors, hooks and logs.
func RedactSecrets(value string) string {
	return redact.Secrets(value)
}
