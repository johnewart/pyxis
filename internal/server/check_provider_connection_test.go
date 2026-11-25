package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/backend"
	bedrockllm "github.com/johnewart/pyxis/internal/llm/bedrock"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestCheckProviderConnection_MissingBinaryIsAnAnswerNotAnError(t *testing.T) {
	// A failed check must come back as a populated response, not a gRPC error:
	// the frontend has to render it and offer the matching remedy.
	if runtime.GOOS == "windows" {
		t.Skip("PATH/HOME isolation assumes a unix layout")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CLAUDE_CLI_PATH", "")

	res, err := (&LlmServer{}).CheckProviderConnection(context.Background(),
		&llmv1.CheckProviderConnectionRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2,
		})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res.GetStatus() != llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_BINARY_NOT_FOUND {
		t.Fatalf("status = %s, want BINARY_NOT_FOUND", res.GetStatus())
	}
	if res.GetNeedsAuth() {
		t.Fatal("needs_auth set for a missing binary; that would offer a sign-in button that fixes nothing")
	}
	if res.GetMessage() == "" {
		t.Fatal("message is empty; the UI has nothing to show")
	}
}

func TestCheckProviderConnection_ClassifiesNotAuthenticated(t *testing.T) {
	// A fake CLI that passes version + --help, then reports an expired login on
	// the first real call -- the exact shape of "installed but signed out".
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI fixture is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  -v) echo '2.1.261 (Claude Code)'; exit 0;;\n" +
		"  -h) echo '--resume --output-format'; exit 0;;\n" +
		"esac\n" +
		"echo 'OAuth token has expired. Please run /login' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := (&LlmServer{}).CheckProviderConnection(context.Background(),
		&llmv1.CheckProviderConnectionRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2,
			Binary:       &bin,
		})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if res.GetStatus() != llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_NOT_AUTHENTICATED {
		t.Fatalf("status = %s, want NOT_AUTHENTICATED (message=%q details=%q)",
			res.GetStatus(), res.GetMessage(), res.GetDetails())
	}
	// This flag is what gates the sign-in affordance; if it is wrong the user
	// gets told to upgrade Claude Code when they just need to log in.
	if !res.GetNeedsAuth() {
		t.Fatal("needs_auth is false for an expired login")
	}
	if res.GetVersion() != "2.1.261" {
		t.Fatalf("version = %q, want 2.1.261", res.GetVersion())
	}
}

func TestCheckProviderConnection_RejectsNonCliProvider(t *testing.T) {
	_, err := (&LlmServer{}).CheckProviderConnection(context.Background(),
		&llmv1.CheckProviderConnectionRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI,
		})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument", status.Code(err))
	}
}

const testRoleARN = "arn:aws:iam::123456789012:role/llmd-test"

type fakeCheckBackend struct {
	backend.Backend
	calls int
	err   error
}

func (f *fakeCheckBackend) Generate(context.Context, string, string, *llmv1.CallOptions, []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	f.calls++
	return &llmv1.AssistantReply{}, f.err
}

func bedrockCheckReq(roleARN string) *llmv1.CheckProviderConnectionRequest {
	b := &llmv1.BedrockLlmConfig{Region: "us-east-1", DefaultModel: proto.String("m")}
	if roleARN != "" {
		b.AssumeRoleArn = proto.String(roleARN)
	}
	return &llmv1.CheckProviderConnectionRequest{
		ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK,
		Config:       &llmv1.CheckProviderConnectionRequest_Bedrock{Bedrock: b},
	}
}

// newCheckServer fakes the provider build, STS and model call but uses the
// real os.LookupEnv; tests control the environment with t.Setenv.
func newCheckServer(t *testing.T, be *fakeCheckBackend, idErr error) *LlmServer {
	t.Helper()
	t.Setenv(bedrockBearerTokenEnv, "")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	return &LlmServer{CheckHooks: &ProviderCheckHooks{
		BuildEntry: func(*store.LlmProviderConfig) (*providers.Entry, error) {
			return &providers.Entry{Backend: be}, nil
		},
		CallerIdentity: func(context.Context, bedrockllm.Config) (*bedrockllm.CallerIdentityResult, error) {
			if idErr != nil {
				return nil, idErr
			}
			return &bedrockllm.CallerIdentityResult{Account: "123456789012", ARN: "arn:aws:sts::123456789012:assumed-role/llmd-test/s", UserID: "AROA:s"}, nil
		},
	}}
}

func TestCheckProviderConnection_APISuccess(t *testing.T) {
	be := &fakeCheckBackend{}
	res, err := newCheckServer(t, be, nil).CheckProviderConnection(context.Background(), bedrockCheckReq(testRoleARN))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.GetStatus() != llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_OK {
		t.Fatalf("status = %s", res.GetStatus())
	}
	if be.calls != 1 {
		t.Fatalf("model calls = %d, want 1", be.calls)
	}
	if res.GetCredentialSource() != llmv1.CredentialSource_CREDENTIAL_SOURCE_ASSUMED_ROLE || res.GetAssumedRoleArn() != testRoleARN {
		t.Fatalf("source=%s role=%q", res.GetCredentialSource(), res.GetAssumedRoleArn())
	}
	if res.GetCallerIdentity().GetAccount() != "123456789012" || res.GetError() != nil {
		t.Fatalf("identity=%v error=%v", res.GetCallerIdentity(), res.GetError())
	}
}

func TestCheckProviderConnection_APIDeniedRole(t *testing.T) {
	be := &fakeCheckBackend{}
	denied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "User is not authorized to perform: sts:AssumeRole on resource"}
	res, err := newCheckServer(t, be, denied).CheckProviderConnection(context.Background(), bedrockCheckReq(testRoleARN))
	if err != nil {
		t.Fatalf("a failed check must not be a gRPC error: %v", err)
	}
	if res.GetStatus() != llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_FAILED {
		t.Fatalf("status = %s, want FAILED", res.GetStatus())
	}
	if res.GetError().GetKind() != llmv1.LlmErrorKind_LLM_ERROR_KIND_PERMISSION_DENIED {
		t.Fatalf("kind = %s, want PERMISSION_DENIED", res.GetError().GetKind())
	}
	// LlmError has no role field; the attempted role is reported on the response.
	if res.GetAssumedRoleArn() != testRoleARN {
		t.Fatalf("assumed_role_arn = %q, want %q", res.GetAssumedRoleArn(), testRoleARN)
	}
	if be.calls != 0 {
		t.Fatalf("model was called %d times after identity failure", be.calls)
	}
}

func TestCheckProviderConnection_APIModelErrorIsFailed(t *testing.T) {
	be := &fakeCheckBackend{err: &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "no"}}
	res, err := newCheckServer(t, be, nil).CheckProviderConnection(context.Background(), bedrockCheckReq(testRoleARN))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.GetStatus() != llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_FAILED || res.GetError() == nil {
		t.Fatalf("status=%s error=%v", res.GetStatus(), res.GetError())
	}
}

func TestCheckProviderConnection_APIBearerTokenWarning(t *testing.T) {
	srv := newCheckServer(t, &fakeCheckBackend{}, nil)
	t.Setenv(bedrockBearerTokenEnv, "secret-token")
	res, err := srv.CheckProviderConnection(context.Background(), bedrockCheckReq(testRoleARN))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.GetCredentialSource() != llmv1.CredentialSource_CREDENTIAL_SOURCE_BEARER_TOKEN {
		t.Fatalf("source = %s", res.GetCredentialSource())
	}
	if len(res.GetWarnings()) != 1 {
		t.Fatalf("warnings = %v, want 1", res.GetWarnings())
	}
	if w := res.GetWarnings()[0]; w == "" || strings.Contains(w, "secret-token") {
		t.Fatalf("warning empty or leaks the token: %q", w)
	}
}

func TestCheckProviderConnection_APIWebIdentity(t *testing.T) {
	srv := newCheckServer(t, &fakeCheckBackend{}, nil)
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/var/run/secrets/token")
	res, err := srv.CheckProviderConnection(context.Background(), bedrockCheckReq(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.GetCredentialSource() != llmv1.CredentialSource_CREDENTIAL_SOURCE_WEB_IDENTITY {
		t.Fatalf("source = %s, want WEB_IDENTITY", res.GetCredentialSource())
	}
}
