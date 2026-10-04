package openai

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"elbot/internal/llm"
)

func newErrorResponse(status int, contentType, body string) *http.Response {
	header := http.Header{}
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestParseErrorKeepsStructuredFields(t *testing.T) {
	resp := newErrorResponse(http.StatusNotFound, "application/json",
		`{"error":{"message":"The model gpt-x does not exist","type":"invalid_request_error","param":null,"code":"model_not_found"}}`)
	err := parseError(resp)
	apiErr, ok := llm.AsAPIError(err)
	if !ok {
		t.Fatalf("parseError did not return an *llm.APIError: %v", err)
	}
	if apiErr.StatusCode != http.StatusNotFound || apiErr.Code != "model_not_found" || apiErr.Type != "invalid_request_error" || apiErr.Param != "" {
		t.Fatalf("structured fields = %+v", apiErr)
	}
	if !apiErr.Deterministic() {
		t.Fatal("model_not_found should be classified as deterministic")
	}
	if !strings.HasPrefix(err.Error(), "HTTP 404: ") {
		t.Fatalf("Error() = %q, want the HTTP 404 prefix", err.Error())
	}
}

func TestParseErrorNormalizesUntypedCode(t *testing.T) {
	resp := newErrorResponse(http.StatusBadRequest, "application/json",
		`{"error":{"message":"invalid value","type":"invalid_request_error","code":123,"param":"messages"}}`)
	apiErr, ok := llm.AsAPIError(parseError(resp))
	if !ok {
		t.Fatal("expected an *llm.APIError")
	}
	if apiErr.Code != "123" {
		t.Fatalf("numeric code = %q, want \"123\"", apiErr.Code)
	}
}

func TestParseErrorHTMLLeavesNoCode(t *testing.T) {
	resp := newErrorResponse(http.StatusBadGateway, "text/html", "<html><body>gateway</body></html>")
	apiErr, ok := llm.AsAPIError(parseError(resp))
	if !ok {
		t.Fatal("expected an *llm.APIError")
	}
	if apiErr.Code != "" || apiErr.Deterministic() {
		t.Fatalf("HTML gateway error must not carry a code or be deterministic: %+v", apiErr)
	}
	if !strings.Contains(apiErr.Error(), "上游返回 HTML") {
		t.Fatalf("Error() = %q, want the HTML hint", apiErr.Error())
	}
}

func TestParseErrorClassifiesVisionUnsupported(t *testing.T) {
	// Some gateways only describe the image rejection in prose. That dialect
	// knowledge lives in the adapter, which is why a category exists at all.
	resp := newErrorResponse(http.StatusBadRequest, "application/json",
		`{"error":{"message":"<400> InternalError.Algo.InvalidParameter: The provided messages input is invalid. The error info is [Unexpected item type in content.]","type":"invalid_parameter_error","code":"invalid_parameter","param":null}}`)
	apiErr, ok := llm.AsAPIError(parseError(resp))
	if !ok {
		t.Fatal("expected an *llm.APIError")
	}
	if apiErr.Category != llm.ErrorCategoryVisionUnsupported {
		t.Fatalf("category = %q, want %q", apiErr.Category, llm.ErrorCategoryVisionUnsupported)
	}
}

func TestParseErrorClassifiesVisionUnsupportedFromBareBody(t *testing.T) {
	// A non-JSON body (plain text gateway) still carries the dialect.
	resp := newErrorResponse(http.StatusBadRequest, "text/plain",
		`InternalError.Algo.InvalidParameter: unexpected item type in content.`)
	apiErr, ok := llm.AsAPIError(parseError(resp))
	if !ok {
		t.Fatal("expected an *llm.APIError")
	}
	if apiErr.Category != llm.ErrorCategoryVisionUnsupported {
		t.Fatalf("category = %q, want %q", apiErr.Category, llm.ErrorCategoryVisionUnsupported)
	}
}

func TestParseErrorVisionCategoryVetoedByUnrelatedParam(t *testing.T) {
	resp := newErrorResponse(http.StatusBadRequest, "application/json",
		`{"error":{"message":"provider image-lab: temperature out of range","type":"invalid_request_error","code":"invalid_parameter","param":"temperature"}}`)
	apiErr, ok := llm.AsAPIError(parseError(resp))
	if !ok {
		t.Fatal("expected an *llm.APIError")
	}
	if apiErr.Category == llm.ErrorCategoryVisionUnsupported {
		t.Fatalf("an unrelated parameter must veto the vision category: %+v", apiErr)
	}
	if apiErr.Category != llm.ErrorCategoryInvalidRequest {
		t.Fatalf("category = %q, want %q", apiErr.Category, llm.ErrorCategoryInvalidRequest)
	}
}

func TestParseErrorModelNameIsNotAVisionSignal(t *testing.T) {
	resp := newErrorResponse(http.StatusNotFound, "application/json",
		`{"error":{"message":"model image-chat-v2 not found","type":"invalid_request_error","code":"","param":null}}`)
	apiErr, ok := llm.AsAPIError(parseError(resp))
	if !ok {
		t.Fatal("expected an *llm.APIError")
	}
	if apiErr.Category != llm.ErrorCategoryModelNotFound {
		t.Fatalf("category = %q, want %q", apiErr.Category, llm.ErrorCategoryModelNotFound)
	}
}

func TestParseErrorClassifiesStatuses(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		category string
	}{
		{"model not found by code", http.StatusBadRequest, `{"error":{"message":"nope","code":"model_not_found"}}`, llm.ErrorCategoryModelNotFound},
		{"auth", http.StatusUnauthorized, `{"error":{"message":"bad key"}}`, llm.ErrorCategoryAuth},
		{"rate limit", http.StatusTooManyRequests, `{"error":{"message":"slow down"}}`, llm.ErrorCategoryRateLimit},
		{"gateway timeout", http.StatusGatewayTimeout, `{"error":{"message":"gateway"}}`, llm.ErrorCategoryTimeout},
		{"server error", http.StatusInternalServerError, `{"error":{"message":"boom"}}`, llm.ErrorCategoryServer},
		{"attributing bad request", http.StatusBadRequest, `{"error":{"message":"bad field","type":"invalid_request_error","param":"max_tokens"}}`, llm.ErrorCategoryInvalidRequest},
		{"bare bad request stays unclassified", http.StatusBadRequest, `{"error":{"message":"something went wrong"}}`, ""},
		{"rate limit mentioning image stays rate limit", http.StatusTooManyRequests, `{"error":{"message":"rate limited while sending image"}}`, llm.ErrorCategoryRateLimit},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resp := newErrorResponse(testCase.status, "application/json", testCase.body)
			apiErr, ok := llm.AsAPIError(parseError(resp))
			if !ok {
				t.Fatal("expected an *llm.APIError")
			}
			if apiErr.Category != testCase.category {
				t.Fatalf("category = %q, want %q (%+v)", apiErr.Category, testCase.category, apiErr)
			}
		})
	}
}
