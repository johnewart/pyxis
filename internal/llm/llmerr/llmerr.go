// Package llmerr classifies provider errors (Anthropic, OpenAI, Bedrock) and builds
// gRPC status errors that carry a sanitized llmv1.LlmError detail.
package llmerr

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/sashabaranov/go-openai"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Classification is the provider-neutral result of classifying an error.
type Classification struct {
	Kind       llmv1.LlmErrorKind
	HTTPStatus int
	RetryAfter time.Duration // 0 when unknown
	Retryable  bool
}

// ToStatus returns a gRPC status error for err with an LlmError detail attached.
// The status message is a fixed string per kind: upstream text (which may contain
// secrets or URLs) is only logged server-side. A nil err returns nil.
func ToStatus(err error, providerType llmv1.LlmProviderType) error {
	if err == nil {
		return nil
	}
	// Existing gRPC statuses and context cancellation pass through unchanged.
	var gs interface{ GRPCStatus() *status.Status }
	if errors.As(err, &gs) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	c := Classify(err, providerType)
	slog.Error("llm provider call failed",
		"provider_type", providerType.String(),
		"kind", c.Kind.String(),
		"provider_status", c.HTTPStatus,
		"error", err)

	code := grpcCode(c.Kind)
	detail := &llmv1.LlmError{
		Kind:           c.Kind,
		ProviderType:   providerType,
		ProviderStatus: int32(c.HTTPStatus),
		Retryable:      c.Retryable,
	}
	if c.RetryAfter > 0 {
		detail.RetryAfter = durationpb.New(c.RetryAfter)
	}
	st := status.New(code, fullMessage(c.Kind, providerType, c.HTTPStatus))
	withDetail, derr := st.WithDetails(detail)
	if derr != nil {
		slog.Error("llm error: attach status detail failed", "error", derr)
		return st.Err()
	}
	return withDetail.Err()
}

// FromStatus reads the LlmError detail that ToStatus attached to err. ok is
// false when err is not a gRPC status carrying that detail.
func FromStatus(err error) (c Classification, ok bool) {
	st, isStatus := status.FromError(err)
	if !isStatus {
		return c, false
	}
	for _, d := range st.Details() {
		if le, isLe := d.(*llmv1.LlmError); isLe {
			c.Kind = le.GetKind()
			c.HTTPStatus = int(le.GetProviderStatus())
			c.Retryable = le.GetRetryable()
			if ra := le.GetRetryAfter(); ra != nil {
				c.RetryAfter = ra.AsDuration()
			}
			return c, true
		}
	}
	return c, false
}

// Classify maps a provider error to a Classification.
func Classify(err error, providerType llmv1.LlmProviderType) Classification {
	var c Classification
	msg := err.Error()
	lower := strings.ToLower(msg)
	c.RetryAfter = parseRetryAfterText(msg)

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		c.Kind = llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT
	case errors.Is(err, context.Canceled):
		c.Kind = llmv1.LlmErrorKind_LLM_ERROR_KIND_UNKNOWN
	default:
		c = classifyProvider(err, c, lower)
	}
	c.Retryable = retryable(c.Kind)
	return c
}

func classifyProvider(err error, c Classification, lower string) Classification {
	var (
		aErr  *anthropic.Error
		oAPI  *openai.APIError
		oReq  *openai.RequestError
		smErr smithy.APIError
	)
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) && re.Response != nil && re.Response.Response != nil {
		c.HTTPStatus = re.Response.StatusCode
		if d := parseRetryAfterHeader(re.Response.Header); d > 0 {
			c.RetryAfter = d
		}
	}
	switch {
	case errors.As(err, &aErr):
		c.HTTPStatus = aErr.StatusCode
		if aErr.Response != nil {
			if d := parseRetryAfterHeader(aErr.Response.Header); d > 0 {
				c.RetryAfter = d
			}
		}
		c.Kind = kindFromHTTP(c.HTTPStatus, lower, "")
	case errors.As(err, &oAPI):
		c.HTTPStatus = oAPI.HTTPStatusCode
		code := ""
		if s, ok := oAPI.Code.(string); ok {
			code = s
		}
		c.Kind = kindFromHTTP(c.HTTPStatus, lower, code)
	case errors.As(err, &oReq):
		c.HTTPStatus = oReq.HTTPStatusCode
		c.Kind = kindFromHTTP(c.HTTPStatus, lower, "")
	case errors.As(err, &smErr):
		c.Kind = kindFromBedrock(smErr.ErrorCode(), lower)
	default:
		var ne interface{ Timeout() bool }
		if errors.As(err, &ne) && ne.Timeout() {
			c.Kind = llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT
		} else {
			c.Kind = llmv1.LlmErrorKind_LLM_ERROR_KIND_UNKNOWN
		}
	}
	return c
}

func isContextLength(lower string) bool {
	for _, s := range []string{"context length", "context_length", "prompt is too long", "too many tokens", "input is too long", "maximum context", "context window"} {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func kindFromHTTP(statusCode int, lower, code string) llmv1.LlmErrorKind {
	if code == "context_length_exceeded" || (statusCode == http.StatusBadRequest && isContextLength(lower)) {
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH
	}
	if code == "content_filter" || code == "content_policy_violation" ||
		strings.Contains(lower, "content_filter") || strings.Contains(lower, "content filter") ||
		strings.Contains(lower, "content policy") || strings.Contains(lower, "content_policy") {
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTENT_FILTER
	}
	if strings.Contains(lower, "overloaded") {
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE
	}
	switch statusCode {
	case http.StatusTooManyRequests:
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED
	case http.StatusUnauthorized:
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_AUTH
	case http.StatusForbidden:
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED
	case http.StatusRequestEntityTooLarge:
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT
	}
	if statusCode >= 400 && statusCode < 500 {
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST
	}
	if statusCode >= 500 && statusCode < 600 {
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE
	}
	return llmv1.LlmErrorKind_LLM_ERROR_KIND_UNKNOWN
}

func kindFromBedrock(code, lower string) llmv1.LlmErrorKind {
	switch code {
	case "ThrottlingException", "ThrottledException", "TooManyRequestsException", "ServiceQuotaExceededException":
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED
	case "ExpiredTokenException", "UnrecognizedClientException", "InvalidSignatureException", "ExpiredToken", "InvalidClientTokenId":
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_AUTH
	case "AccessDeniedException", "AccessDenied":
		// Also covers the sts:AssumeRole denial: the caller is authenticated
		// but not allowed to assume the configured role.
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED
	case "ServiceUnavailableException", "InternalServerException", "ModelNotReadyException":
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE
	case "ModelTimeoutException":
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT
	case "ValidationException":
		if isContextLength(lower) {
			return llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH
		}
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST
	case "ModelErrorException", "ResourceNotFoundException":
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST
	}
	if strings.Contains(lower, "assumerole") {
		return llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED
	}
	return llmv1.LlmErrorKind_LLM_ERROR_KIND_UNKNOWN
}

func retryable(k llmv1.LlmErrorKind) bool {
	switch k {
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED,
		llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE,
		llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT:
		return true
	}
	return false
}

func grpcCode(k llmv1.LlmErrorKind) codes.Code {
	switch k {
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED:
		return codes.ResourceExhausted
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_AUTH:
		return codes.Unauthenticated
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED:
		return codes.PermissionDenied
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH,
		llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST:
		return codes.InvalidArgument
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTENT_FILTER:
		return codes.InvalidArgument
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE:
		return codes.Unavailable
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT:
		return codes.DeadlineExceeded
	}
	return codes.Internal
}

// fullMessage appends the provider type and, when non-zero, the provider status
// code to the fixed per-kind message. Only enum names and integers are used.
func fullMessage(k llmv1.LlmErrorKind, p llmv1.LlmProviderType, httpStatus int) string {
	if httpStatus != 0 {
		return message(k) + " (provider=" + p.String() + ", status=" + strconv.Itoa(httpStatus) + ")"
	}
	return message(k) + " (provider=" + p.String() + ")"
}

// message returns a fixed, client-safe message for a kind.
func message(k llmv1.LlmErrorKind) string {
	switch k {
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_RATE_LIMITED:
		return "LLM provider rate limit exceeded"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_AUTH:
		return "LLM provider authentication failed"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED:
		return "LLM provider denied permission"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTEXT_LENGTH:
		return "request exceeds the model context length"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_CONTENT_FILTER:
		return "request or response blocked by content filter"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_UNAVAILABLE:
		return "LLM provider is unavailable"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_TIMEOUT:
		return "LLM provider call timed out"
	case llmv1.LlmErrorKind_LLM_ERROR_KIND_INVALID_REQUEST:
		return "LLM provider rejected the request"
	}
	return "LLM provider call failed"
}

func parseRetryAfterHeader(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

var retryTextRe = regexp.MustCompile(`(?i)(?:try again|retry) in (\d+(?:\.\d+)?)\s*(ms|s|m)\b`)

// parseRetryAfterText extracts hints like "Please try again in 1.5s" or "retry in 200ms".
func parseRetryAfterText(msg string) time.Duration {
	m := retryTextRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n <= 0 {
		return 0
	}
	switch strings.ToLower(m[2]) {
	case "ms":
		return time.Duration(n * float64(time.Millisecond))
	case "m":
		return time.Duration(n * float64(time.Minute))
	}
	return time.Duration(n * float64(time.Second))
}
