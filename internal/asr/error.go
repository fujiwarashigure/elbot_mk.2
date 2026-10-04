package asr

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPError is a provider or transport failure. StatusCode 0 means the
// request never produced an HTTP response.
type HTTPError struct {
	StatusCode int
	Message    string
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "asr: request failed"
	}
	if e.StatusCode == 0 {
		return "asr: request failed: " + e.Message
	}
	if e.Message == "" {
		return fmt.Sprintf("asr: provider returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("asr: provider returned HTTP %d: %s", e.StatusCode, e.Message)
}

func retryable(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return false
	}
	if httpErr.StatusCode == 0 {
		return true
	}
	return httpErr.StatusCode == http.StatusTooManyRequests || httpErr.StatusCode >= 500
}

func deterministicError(err error) bool {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return false
	}
	switch httpErr.StatusCode {
	case 0, http.StatusTooManyRequests:
		return false
	}
	return httpErr.StatusCode >= 400 && httpErr.StatusCode < 500
}

func newHTTPError(resp *http.Response, body []byte) *HTTPError {
	httpErr := &HTTPError{StatusCode: resp.StatusCode, Message: providerMessage(body)}
	if retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After")); retryAfter != "" {
		if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
			httpErr.RetryAfter = time.Duration(seconds) * time.Second
		} else if when, err := http.ParseTime(retryAfter); err == nil {
			if delay := time.Until(when); delay > 0 {
				httpErr.RetryAfter = delay
			}
		}
	}
	if httpErr.Message == "" {
		httpErr.Message = boundedMessage(strings.TrimSpace(string(body)))
	}
	return httpErr
}

func providerMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	message := firstNonEmpty(payload.Error.Message, payload.Message)
	if message == "" {
		return ""
	}
	if code := strings.TrimSpace(payload.Error.Code); code != "" {
		return code + ": " + boundedMessage(message)
	}
	return boundedMessage(message)
}
