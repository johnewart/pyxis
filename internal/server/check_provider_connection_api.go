package server

import (
	"context"
	"os"
	"strings"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	bedrockllm "github.com/johnewart/pyxis/internal/llm/bedrock"
	"github.com/johnewart/pyxis/internal/llm/llmerr"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	defaultAPICheckTimeout = 30 * time.Second
	bedrockBearerTokenEnv  = "AWS_BEARER_TOKEN_BEDROCK"
	checkProviderID        = "connection-check"
)

// ProviderCheckHooks are the clients an API provider check talks to. Every nil
// field falls back to the real implementation used by registration.
type ProviderCheckHooks struct {
	// BuildEntry builds the unregistered provider (default: the registration builder).
	BuildEntry func(*store.LlmProviderConfig) (*providers.Entry, error)
	// CallerIdentity asks STS who the Bedrock credentials belong to.
	CallerIdentity func(ctx context.Context, cfg bedrockllm.Config) (*bedrockllm.CallerIdentityResult, error)
	// LookupEnv reads an environment variable (default: os.LookupEnv).
	LookupEnv func(string) (string, bool)
}

func (s *LlmServer) checkHooks() ProviderCheckHooks {
	var h ProviderCheckHooks
	if s.CheckHooks != nil {
		h = *s.CheckHooks
	}
	if h.BuildEntry == nil {
		h.BuildEntry = providers.BuildEntryFromProviderConfig
	}
	if h.CallerIdentity == nil {
		h.CallerIdentity = bedrockllm.ResolveCallerIdentity
	}
	if h.LookupEnv == nil {
		h.LookupEnv = os.LookupEnv
	}
	return h
}

func webIdentityTokenFileSet(h ProviderCheckHooks) bool {
	v, _ := h.LookupEnv("AWS_WEB_IDENTITY_TOKEN_FILE")
	return strings.TrimSpace(v) != ""
}

// isAPICheckRequest reports whether the request carries an API-provider config
// (anything in the config oneof except the CLI-backed ones).
func isAPICheckRequest(req *llmv1.CheckProviderConnectionRequest) bool {
	switch req.GetConfig().(type) {
	case nil, *llmv1.CheckProviderConnectionRequest_ClaudeCode, *llmv1.CheckProviderConnectionRequest_CursorCli:
		return false
	}
	return true
}

// registerRequestFromCheck maps the check request onto the registration request
// so both go through the same config-building and validation code.
func registerRequestFromCheck(req *llmv1.CheckProviderConnectionRequest) *llmv1.RegisterLlmProviderRequest {
	out := &llmv1.RegisterLlmProviderRequest{ProviderId: checkProviderID, Type: req.GetProviderType()}
	switch c := req.GetConfig().(type) {
	case *llmv1.CheckProviderConnectionRequest_Anthropic:
		out.Config = &llmv1.RegisterLlmProviderRequest_Anthropic{Anthropic: c.Anthropic}
	case *llmv1.CheckProviderConnectionRequest_Openai:
		out.Config = &llmv1.RegisterLlmProviderRequest_Openai{Openai: c.Openai}
	case *llmv1.CheckProviderConnectionRequest_Ollama:
		out.Config = &llmv1.RegisterLlmProviderRequest_Ollama{Ollama: c.Ollama}
	case *llmv1.CheckProviderConnectionRequest_Bedrock:
		out.Config = &llmv1.RegisterLlmProviderRequest_Bedrock{Bedrock: c.Bedrock}
	case *llmv1.CheckProviderConnectionRequest_Gemini:
		out.Config = &llmv1.RegisterLlmProviderRequest_Gemini{Gemini: c.Gemini}
	case *llmv1.CheckProviderConnectionRequest_Openrouter:
		out.Config = &llmv1.RegisterLlmProviderRequest_Openrouter{Openrouter: c.Openrouter}
	}
	return out
}

// failedCheck builds the FAILED answer for err. The message is fixed text per
// error kind; the upstream error is only logged (it may echo secrets).
func failedCheck(resp *llmv1.CheckProviderConnectionResponse, err error, pt llmv1.LlmProviderType, stage string) *llmv1.CheckProviderConnectionResponse {
	c := llmerr.Classify(err, pt)
	llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
		"check provider connection failed: type=%s stage=%s kind=%s provider_status=%d: %v",
		pt, stage, c.Kind, c.HTTPStatus, err)
	le := &llmv1.LlmError{
		Kind:           c.Kind,
		ProviderType:   pt,
		ProviderStatus: int32(c.HTTPStatus),
		Retryable:      c.Retryable,
	}
	if c.RetryAfter > 0 {
		le.RetryAfter = durationpb.New(c.RetryAfter)
	}
	resp.Status = llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_FAILED
	resp.Error = le
	resp.NeedsAuth = false
	resp.Message = "The provider check failed (" + stage + "): " + c.Kind.String()
	return resp
}

// checkAPIProvider tests an API provider config with one small model call. It
// builds the provider the way registration does but never registers or saves it.
func (s *LlmServer) checkAPIProvider(ctx context.Context, req *llmv1.CheckProviderConnectionRequest) (*llmv1.CheckProviderConnectionResponse, error) {
	pt := req.GetProviderType()
	cfg, err := providers.BuildProviderConfigFromRequest(registerRequestFromCheck(req))
	if err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"check provider connection rejected: type=%s invalid config: %v", pt, err)
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	hooks := s.checkHooks()
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo, "check provider connection: type=%s (api config)", pt)

	resp := &llmv1.CheckProviderConnectionResponse{
		CredentialSource: llmv1.CredentialSource_CREDENTIAL_SOURCE_API_KEY,
	}
	var bedrockCfg *bedrockllm.Config
	if b := cfg.Bedrock; b != nil {
		bedrockCfg = &bedrockllm.Config{
			Region:          strings.TrimSpace(b.Region),
			Profile:         strings.TrimSpace(b.Profile),
			AssumeRoleARN:   strings.TrimSpace(b.AssumeRoleARN),
			RoleSessionName: strings.TrimSpace(b.RoleSessionName),
			ExternalID:      strings.TrimSpace(b.ExternalID),
		}
		switch tok, _ := hooks.LookupEnv(bedrockBearerTokenEnv); {
		case strings.TrimSpace(tok) != "":
			resp.CredentialSource = llmv1.CredentialSource_CREDENTIAL_SOURCE_BEARER_TOKEN
			resp.Warnings = append(resp.Warnings, bedrockBearerTokenEnv+
				" is set; Bedrock uses the bearer token instead of IAM credentials, so the profile and role settings do not apply to model calls")
		case bedrockCfg.AssumeRoleARN != "":
			resp.CredentialSource = llmv1.CredentialSource_CREDENTIAL_SOURCE_ASSUMED_ROLE
			resp.AssumedRoleArn = bedrockCfg.AssumeRoleARN
		case webIdentityTokenFileSet(hooks):
			resp.CredentialSource = llmv1.CredentialSource_CREDENTIAL_SOURCE_WEB_IDENTITY
		case bedrockCfg.Profile != "":
			resp.CredentialSource = llmv1.CredentialSource_CREDENTIAL_SOURCE_PROFILE
		default:
			resp.CredentialSource = llmv1.CredentialSource_CREDENTIAL_SOURCE_DEFAULT_CHAIN
		}
	}
	if cfg.Ollama != nil {
		resp.CredentialSource = llmv1.CredentialSource_CREDENTIAL_SOURCE_NONE
	}

	timeout := defaultAPICheckTimeout
	if ms := req.GetTimeoutMs(); ms > 0 {
		timeout = time.Duration(ms) * time.Millisecond
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ent, err := hooks.BuildEntry(cfg)
	if err != nil {
		return failedCheck(resp, err, pt, "build provider"), nil
	}

	if bedrockCfg != nil {
		id, err := hooks.CallerIdentity(cctx, *bedrockCfg)
		if err != nil {
			return failedCheck(resp, err, pt, "caller identity"), nil
		}
		resp.CallerIdentity = &llmv1.CallerIdentity{Account: id.Account, Arn: id.ARN, UserId: id.UserID}
	}

	if ent == nil || ent.Backend == nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"check provider connection: type=%s built provider has no backend", pt)
		return nil, status.Error(codes.Internal, "built provider has no backend")
	}
	if _, err := ent.Backend.Generate(cctx, "", "Reply with the single word: ok",
		&llmv1.CallOptions{MaxTokens: 16}, nil); err != nil {
		return failedCheck(resp, err, pt, "model call"), nil
	}

	resp.Status = llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_OK
	resp.Message = "Connected. The provider accepted a test request."
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"check provider connection result: type=%s status=%s credential_source=%s", pt, resp.Status, resp.CredentialSource)
	return resp, nil
}
