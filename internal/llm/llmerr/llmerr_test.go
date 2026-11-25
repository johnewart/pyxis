package llmerr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusTable(t *testing.T) {
	const (
		anth = llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC
		oai  = llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI
		bed  = llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK
	)
	hdr := http.Header{}
	hdr.Set("Retry-After", "7")
	cases := []struct {
		name       string
		err        error
		provider   llmv1.LlmProviderType
		code       codes.Code
		kind       llmv1.LlmErrorKind
		retryable  bool
		retryAfter time.Duration
	}{
		{"anthropic rate limit", anthErr(429, hdr), anth,
			codes.ResourceExhausted, llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED, true, 7 * time.Second},
		{"anthropic auth", anthErr(401, nil), anth,
			codes.Unauthenticated, llmv1.LlmErrorKind_LLM_ERROR_KIND_AUTH, false, 0},
		{"anthropic overloaded", anthErr(529, nil), anth,
			codes.Unavailable, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE, true, 0},
		{"openai rate limit with text hint", &openai.APIError{HTTPStatusCode: 429, Message: "Rate limit. Please try again in 1.5s."}, oai,
			codes.ResourceExhausted, llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED, true, 1500 * time.Millisecond},
		{"openai context length", &openai.APIError{HTTPStatusCode: 400, Code: "context_length_exceeded", Message: "too long"}, oai,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH, false, 0},
		{"openai request error 503", &openai.RequestError{HTTPStatusCode: 503, Err: errors.New("down")}, oai,
			codes.Unavailable, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE, true, 0},
		{"bedrock throttling", &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow down"}, bed,
			codes.ResourceExhausted, llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED, true, 0},
		{"bedrock timeout", &smithy.GenericAPIError{Code: "ModelTimeoutException"}, bed,
			codes.DeadlineExceeded, llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT, true, 0},
		{"bedrock assume role denied", fmt.Errorf("get credentials: %w", &smithy.GenericAPIError{
			Code: "AccessDenied", Message: "User is not authorized to perform: sts:AssumeRole on resource"}), bed,
			codes.PermissionDenied, llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED, false, 0},
		{"anthropic 403", anthErr(403, nil), anth,
			codes.PermissionDenied, llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED, false, 0},
		{"anthropic 500", anthErr(500, nil), anth,
			codes.Unavailable, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE, true, 0},
		{"anthropic 501", anthErr(501, nil), anth,
			codes.Unavailable, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE, true, 0},
		{"anthropic overloaded text without 529", fmt.Errorf("overloaded_error: Overloaded: %w", anthErr(200, nil)), anth,
			codes.Unavailable, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE, true, 0},
		{"anthropic context length", fmt.Errorf("prompt is too long: 250000 tokens: %w", anthErr(400, nil)), anth,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH, false, 0},
		{"anthropic content filter", fmt.Errorf("blocked by content filter: %w", anthErr(400, nil)), anth,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTENT_FILTER, false, 0},
		{"openai content filter code", &openai.APIError{HTTPStatusCode: 400, Code: "content_filter", Message: "x"}, oai,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTENT_FILTER, false, 0},
		{"openai content policy text", &openai.APIError{HTTPStatusCode: 400, Message: "violates our content policy"}, oai,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTENT_FILTER, false, 0},
		{"openai 401", &openai.APIError{HTTPStatusCode: 401, Message: "bad"}, oai,
			codes.Unauthenticated, llmv1.LlmErrorKind_LLM_ERROR_KIND_AUTH, false, 0},
		{"openai 403", &openai.APIError{HTTPStatusCode: 403, Message: "no"}, oai,
			codes.PermissionDenied, llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED, false, 0},
		{"openai 409", &openai.APIError{HTTPStatusCode: 409, Message: "conflict"}, oai,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST, false, 0},
		{"bedrock service unavailable", &smithy.GenericAPIError{Code: "ServiceUnavailableException", Message: "x"}, bed,
			codes.Unavailable, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE, true, 0},
		{"bedrock access denied", &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "no model access"}, bed,
			codes.PermissionDenied, llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED, false, 0},
		{"bedrock validation", &smithy.GenericAPIError{Code: "ValidationException", Message: "bad field"}, bed,
			codes.InvalidArgument, llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST, false, 0},
		{"bedrock throttling with Retry-After header", &smithyhttp.ResponseError{
			Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 429, Header: hdr}},
			Err:      &smithy.GenericAPIError{Code: "ThrottlingException", Message: "slow"}}, bed,
			codes.ResourceExhausted, llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED, true, 7 * time.Second},
		{"deadline exceeded", fmt.Errorf("call: %w", context.DeadlineExceeded), oai,
			codes.DeadlineExceeded, llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT, true, 0},
		{"unknown", errors.New("boom"), bed,
			codes.Internal, llmv1.LlmErrorKind_LLM_ERROR_KIND_UNKNOWN, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, ok := status.FromError(ToStatus(tc.err, tc.provider))
			if !ok {
				t.Fatal("not a status error")
			}
			if st.Code() != tc.code {
				t.Errorf("code = %v, want %v", st.Code(), tc.code)
			}
			d := findDetail(t, st)
			if d.Kind != tc.kind || d.Retryable != tc.retryable || d.ProviderType != tc.provider {
				t.Errorf("detail = %v", d)
			}
			var got time.Duration
			if d.RetryAfter != nil {
				got = d.RetryAfter.AsDuration()
			}
			if got != tc.retryAfter {
				t.Errorf("retry_after = %v, want %v", got, tc.retryAfter)
			}
		})
	}
}

func anthErr(code int, h http.Header) *anthropic.Error {
	u, _ := url.Parse("https://api.anthropic.com/v1/messages")
	if h == nil {
		h = http.Header{}
	}
	return &anthropic.Error{
		StatusCode: code,
		Request:    &http.Request{Method: http.MethodPost, URL: u},
		Response:   &http.Response{StatusCode: code, Header: h},
	}
}

func findDetail(t *testing.T, st *status.Status) *llmv1.LlmError {
	t.Helper()
	for _, d := range st.Details() {
		if le, ok := d.(*llmv1.LlmError); ok {
			return le
		}
	}
	t.Fatal("no LlmError detail")
	return nil
}

func TestToStatusSanitizes(t *testing.T) {
	const secret = "sk-ant-SECRET123"
	const url = "https://internal.example.com/v1/messages"
	err := &openai.APIError{HTTPStatusCode: 401, Message: "bad key " + secret + " calling " + url}
	st, _ := status.FromError(ToStatus(err, llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI))
	d := findDetail(t, st)
	all := st.Message() + " " + d.String() + " " + st.Proto().String()
	for _, bad := range []string{secret, url, "internal.example.com"} {
		if strings.Contains(all, bad) {
			t.Errorf("leaked %q in %q", bad, all)
		}
	}
}

func TestDetailSurvivesFromError(t *testing.T) {
	wrapped := fmt.Errorf("wrap: %w", ToStatus(anthErr(429, nil), llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC))
	st, ok := status.FromError(wrapped)
	if !ok {
		t.Fatal("FromError failed on wrapped status")
	}
	if d := findDetail(t, st); d.Kind != llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED {
		t.Errorf("kind = %v", d.Kind)
	}
}

func TestToStatusPassThrough(t *testing.T) {
	p := llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI
	grpcErr := status.Error(codes.NotFound, "nope")
	if got := ToStatus(grpcErr, p); got != grpcErr {
		t.Errorf("grpc status changed: %v", got)
	}
	canceled := fmt.Errorf("wrap: %w", context.Canceled)
	if got := ToStatus(canceled, p); got != canceled {
		t.Errorf("canceled changed: %v", got)
	}
}

func TestMessageIncludesProviderAndStatus(t *testing.T) {
	st, _ := status.FromError(ToStatus(anthErr(429, nil), llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC))
	want := "LLM provider rate limit exceeded (provider=LLM_PROVIDER_TYPE_ANTHROPIC, status=429)"
	if st.Message() != want {
		t.Errorf("message = %q, want %q", st.Message(), want)
	}
	// Zero provider status: only the provider type is appended.
	st, _ = status.FromError(ToStatus(&smithy.GenericAPIError{Code: "ThrottlingException"}, llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK))
	if m := st.Message(); !strings.Contains(m, "provider=LLM_PROVIDER_TYPE_BEDROCK") || strings.Contains(m, "status=") {
		t.Errorf("unexpected message %q", m)
	}
}

func TestToStatusNil(t *testing.T) {
	if ToStatus(nil, llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI) != nil {
		t.Error("expected nil")
	}
}
