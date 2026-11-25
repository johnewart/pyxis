package server

import (
	"context"
	"strings"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// probeReasonToStatus maps the probe's classification onto the wire enum.
func probeReasonToStatus(r claudecodeclillm.ProbeReason) llmv1.ProviderConnectionStatus {
	switch r {
	case claudecodeclillm.ProbeReasonOK:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_OK
	case claudecodeclillm.ProbeReasonBinaryNotFound:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_BINARY_NOT_FOUND
	case claudecodeclillm.ProbeReasonVersionTooOld:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_VERSION_TOO_OLD
	case claudecodeclillm.ProbeReasonNotAuthenticated:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_NOT_AUTHENTICATED
	case claudecodeclillm.ProbeReasonResumeUnsupported:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_RESUME_UNSUPPORTED
	case claudecodeclillm.ProbeReasonTimedOut:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_TIMED_OUT
	default:
		return llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_FAILED
	}
}

// CheckProviderConnection validates a CLI provider config without registering it.
//
// A failed check is NOT a gRPC error: "your CLI is not signed in" is a normal,
// expected answer that the caller has to render (and act on), not a transport
// failure. Only a malformed request errors.
func (s *LlmServer) CheckProviderConnection(ctx context.Context, req *llmv1.CheckProviderConnectionRequest) (*llmv1.CheckProviderConnectionResponse, error) {
	// An unset provider_type is a different fault from a non-CLI one, and saying
	// "UNSPECIFIED is not backed by a CLI" reads as a non-sequitur to whoever hit
	// it -- the field simply never arrived (a client that maps an unknown provider
	// name to the zero value is the usual cause).
	if req.GetProviderType() == llmv1.LlmProviderType_LLM_PROVIDER_TYPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument,
			"provider_type is required and was not set (got LLM_PROVIDER_TYPE_UNSPECIFIED); "+
				"pass the CLI-backed provider type to check, e.g. LLM_PROVIDER_TYPE_CLAUDE_CODE_V2")
	}
	if isAPICheckRequest(req) {
		return s.checkAPIProvider(ctx, req)
	}
	kind, ok := discoveryKindFor(req.GetProviderType())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument,
			"provider type %s is not backed by a CLI, so there is no connection to check",
			req.GetProviderType())
	}
	// Cursor has no resume/auth probe yet; saying so plainly beats reporting a
	// misleading OK for a check that never ran.
	if kind != claudeDiscoveryKind {
		return nil, status.Errorf(codes.Unimplemented,
			"connection checking is not implemented for provider type %s yet", req.GetProviderType())
	}

	timeout := time.Duration(req.GetTimeoutMs()) * time.Millisecond
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"check provider connection: type=%s binary=%q config_dir=%q",
		req.GetProviderType(), req.GetBinary(), req.GetConfigDir())

	r := claudecodeclillm.ProbeClaudeCode(claudecodeclillm.ProbeOptions{
		Binary:    strings.TrimSpace(req.GetBinary()),
		Cwd:       strings.TrimSpace(req.GetCwd()),
		ConfigDir:  strings.TrimSpace(req.GetConfigDir()),
		OAuthToken: strings.TrimSpace(req.GetOauthToken()),
		Timeout:    timeout,
	})

	st := probeReasonToStatus(r.Reason)
	if r.OK {
		st = llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_OK
	}
	msg := strings.TrimSpace(r.Error)
	if r.OK {
		msg = "Connected. The Claude Code CLI is installed, signed in, and supports session resume."
	} else if msg == "" {
		msg = claudecodeclillm.ClaudeV2ProbeUserFacingError()
	}

	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"check provider connection result: status=%s binary=%s version=%s", st, r.Binary, r.Version)

	return &llmv1.CheckProviderConnectionResponse{
		Status:    st,
		Binary:    r.Binary,
		Version:   r.Version,
		Message:   msg,
		Details:   strings.TrimSpace(r.Details),
		NeedsAuth: st == llmv1.ProviderConnectionStatus_PROVIDER_CONNECTION_STATUS_NOT_AUTHENTICATED,
	}, nil
}
