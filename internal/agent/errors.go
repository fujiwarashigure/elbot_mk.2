package agent

import (
	"fmt"

	"elbot/internal/redact"
)

const maxUserErrorRunes = 400

type userErrorDetails struct {
	ID   string
	Safe string
	Text string
}

// newUserErrorDetails builds a redacted, bounded user-facing error message and
// keeps an id that can be correlated with audit/runtime logs.
func newUserErrorDetails(prefix string, err error) userErrorDetails {
	id := redact.NewErrorID()
	safe := redact.Summarize(redact.Error(err), maxUserErrorRunes)
	text := fmt.Sprintf("%s（error_id=%s）", safe, id)
	if prefix != "" {
		text = fmt.Sprintf("%s：%s（error_id=%s）", prefix, safe, id)
	}
	return userErrorDetails{ID: id, Safe: safe, Text: text}
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", redact.Summarize(err.Error(), maxUserErrorRunes))
}
