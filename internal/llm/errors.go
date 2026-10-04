package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// APIError is a structured upstream API failure. Adapters keep the HTTP status
// plus the upstream error code/type/param so callers can classify failures
// (retry, vision fallback, negative cache) without matching on message text.
//
// Message must always be a safe summary: it is user-visible in places and must
// never contain credentials, request bodies or raw provider payloads.
type APIError struct {
	StatusCode int
	Code       string
	Type       string
	Param      string
	Message    string
	// Category is the adapter-controlled failure class (see the
	// ErrorCategory* constants). Adapters derive it from the status code plus
	// the machine-readable code/type/param fields, so callers branch on
	// Category instead of pattern-matching Message, which is free-form provider
	// text and is also user-visible. Empty means "not classified".
	Category string
	// Cause preserves the underlying transport/read error so errors.Is/As keep
	// working (for example errors.Is(err, context.Canceled)).
	Cause error
}

// Failure categories reported in APIError.Category. They are stable internal
// identifiers: never show them to users and never map them back from Message.
const (
	// ErrorCategoryVisionUnsupported means the upstream rejected the image
	// content part, i.e. the selected model cannot accept images.
	ErrorCategoryVisionUnsupported = "vision_unsupported"
	// ErrorCategoryModelNotFound means the named model/endpoint does not exist.
	ErrorCategoryModelNotFound = "model_not_found"
	// ErrorCategoryInvalidRequest means the upstream attributed the failure to a
	// specific request field.
	ErrorCategoryInvalidRequest = "invalid_request"
	// ErrorCategoryAuth means the credential was rejected.
	ErrorCategoryAuth = "auth"
	// ErrorCategoryRateLimit means the account/endpoint is being throttled.
	ErrorCategoryRateLimit = "rate_limit"
	// ErrorCategoryTimeout means the upstream/gateway timed the request out.
	ErrorCategoryTimeout = "timeout"
	// ErrorCategoryServer means the upstream failed server-side.
	ErrorCategoryServer = "server_error"
)

// Unwrap exposes the underlying cause to errors.Is/errors.As.
func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Error keeps the historical "HTTP <status>: <message>" shape so existing
// string-based logging and tests continue to read the same way.
func (e *APIError) Error() string {
	if e == nil {
		return "API error"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "request failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, message)
	}
	return message
}

// AsAPIError extracts a structured APIError from an error chain.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// Deterministic reports whether the failure is a stable request-level failure
// that should reproduce for the same request, which makes it a candidate for a
// short-lived negative cache.
//
// Only explicitly identified model or parameter failures qualify. A bare 400 is
// ambiguous across OpenAI-compatible gateways (some use it for transient
// upstream problems) and must not be treated as deterministic.
func (e *APIError) Deterministic() bool {
	if e == nil {
		return false
	}
	// A canceled or timed-out request never produced a stable server-side
	// verdict, even if a gateway attached a 4xx status before the body read
	// failed. Cancellation must win over the status code so a caller giving up
	// can never poison the negative cache for later requests.
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return false
	}
	switch e.StatusCode {
	case 400, 404, 422:
	default:
		return false
	}
	if isDeterministicCode(e.Code) {
		return true
	}
	// A parameter-attributed invalid request is explicit enough even when the
	// gateway omits a code, but only when the upstream names the offending field.
	if strings.EqualFold(strings.TrimSpace(e.Type), "invalid_request_error") && strings.TrimSpace(e.Param) != "" {
		return true
	}
	return false
}

// IsModelNotFoundCode reports whether a machine-readable upstream code names a
// missing or unknown model. Adapters use it to fill APIError.Category, and
// Deterministic uses it to keep the negative cache narrow.
func IsModelNotFoundCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "model_not_found", "model_not_available", "unknown_model", "no_such_model", "model_does_not_exist":
		return true
	default:
		return false
	}
}

// IsInvalidParameterCode reports whether a machine-readable upstream code
// attributes the failure to a specific request field.
func IsInvalidParameterCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_parameter", "invalid_value", "unknown_parameter",
		"unsupported_value", "missing_required_parameter", "invalid_type":
		return true
	default:
		return false
	}
}

func isDeterministicCode(code string) bool {
	return IsModelNotFoundCode(code) || IsInvalidParameterCode(code)
}
