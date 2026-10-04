package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestAPIErrorErrorFormat(t *testing.T) {
	cases := []struct {
		name string
		err  *APIError
		want string
	}{
		{"status and message", &APIError{StatusCode: 400, Message: "bad request"}, "HTTP 400: bad request"},
		{"empty message", &APIError{StatusCode: 500}, "HTTP 500: request failed"},
		{"no status", &APIError{Message: "boom"}, "boom"},
	}
	for _, testCase := range cases {
		if got := testCase.err.Error(); got != testCase.want {
			t.Fatalf("%s: Error() = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestAsAPIErrorUnwrapsCause(t *testing.T) {
	apiErr := &APIError{StatusCode: 400, Code: "model_not_found", Message: "missing"}
	wrapped := fmt.Errorf("chat: %w", apiErr)
	got, ok := AsAPIError(wrapped)
	if !ok || got != apiErr {
		t.Fatalf("AsAPIError = (%v, %v), want the original APIError", got, ok)
	}
	if _, ok := AsAPIError(errors.New("plain")); ok {
		t.Fatal("AsAPIError accepted a plain error")
	}
}

func TestAPIErrorKeepsCauseForErrorsIs(t *testing.T) {
	err := &APIError{StatusCode: 400, Message: "read failed", Cause: errors.New("boom")}
	if !errors.Is(err, err.Cause) {
		t.Fatal("APIError.Unwrap did not expose the cause")
	}
}

func TestAPIErrorDeterministic(t *testing.T) {
	cases := []struct {
		name string
		err  *APIError
		want bool
	}{
		{"explicit model_not_found", &APIError{StatusCode: 404, Code: "model_not_found"}, true},
		{"explicit invalid parameter code", &APIError{StatusCode: 400, Code: "invalid_parameter"}, true},
		{"param attributed invalid request", &APIError{StatusCode: 400, Type: "invalid_request_error", Param: "temperature"}, true},
		{"bare 400 without code", &APIError{StatusCode: 400, Message: "bad request"}, false},
		{"400 with unknown code", &APIError{StatusCode: 400, Code: "something_new"}, false},
		{"429 is transient", &APIError{StatusCode: 429, Code: "model_not_found"}, false},
		{"500 is transient", &APIError{StatusCode: 500, Code: "invalid_parameter"}, false},
		{"auth failure is not cached", &APIError{StatusCode: 401, Code: "invalid_parameter"}, false},
		{"canceled request wins over status", &APIError{StatusCode: 404, Code: "model_not_found", Cause: context.Canceled}, false},
		{"timed-out request wins over status", &APIError{StatusCode: 400, Code: "invalid_parameter", Cause: context.DeadlineExceeded}, false},
		{"wrapped cancellation cause", &APIError{StatusCode: 422, Code: "invalid_value", Cause: fmt.Errorf("read: %w", context.Canceled)}, false},
		{"unrelated cause stays deterministic", &APIError{StatusCode: 404, Code: "model_not_found", Cause: errors.New("boom")}, true},
		{"nil", nil, false},
	}
	for _, testCase := range cases {
		if got := testCase.err.Deterministic(); got != testCase.want {
			t.Fatalf("%s: Deterministic() = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

func TestExportedErrorCodeHelpers(t *testing.T) {
	if !IsModelNotFoundCode(" model_does_not_exist ") || !IsModelNotFoundCode("MODEL_NOT_FOUND") {
		t.Fatal("model-not-found codes must be recognized regardless of case/space")
	}
	if IsModelNotFoundCode("invalid_parameter") || IsInvalidParameterCode("model_not_found") {
		t.Fatal("the two code families must stay distinct")
	}
	if !IsInvalidParameterCode("invalid_type") || !IsInvalidParameterCode("MISSING_REQUIRED_PARAMETER") {
		t.Fatal("parameter codes must be recognized regardless of case")
	}
	if IsModelNotFoundCode("") || IsInvalidParameterCode("") {
		t.Fatal("an empty code must not classify")
	}
}
