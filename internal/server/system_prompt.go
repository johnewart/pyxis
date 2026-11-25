package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/systemprompt"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const metadataSystemPromptSectionsKey = "llmd-system-prompt-sections"

type wireSystemPromptSection struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

type systemPromptSectionSource string

const (
	systemPromptSectionSourceProto    systemPromptSectionSource = "proto"
	systemPromptSectionSourceMetadata systemPromptSectionSource = "metadata"
	// systemPromptSectionSourceSession is the session's persisted EnsureSession
	// default, used only when the ChatStreamRequest itself carries no sections
	// (on proto or metadata) for this turn.
	systemPromptSectionSourceSession systemPromptSectionSource = "session_default"
)

func normalizeSystemPromptSections(items []systemprompt.Section) []systemprompt.Section {
	if len(items) == 0 {
		return nil
	}
	out := make([]systemprompt.Section, 0, len(items))
	for _, s := range items {
		id := strings.TrimSpace(s.ID)
		content := strings.TrimSpace(s.Content)
		if id == "" || content == "" {
			continue
		}
		out = append(out, systemprompt.Section{ID: id, Content: content, Tags: s.Tags})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func systemPromptSectionsFromProto(req *llmv1.ChatStreamRequest) []systemprompt.Section {
	if req == nil {
		return nil
	}
	proto := req.GetSystemPromptSections()
	if len(proto) == 0 {
		return nil
	}
	items := make([]systemprompt.Section, 0, len(proto))
	for _, s := range proto {
		if s == nil {
			continue
		}
		items = append(items, systemprompt.Section{
			ID:      s.GetId(),
			Content: s.GetContent(),
			Tags:    s.GetTags(),
		})
	}
	return normalizeSystemPromptSections(items)
}

func systemPromptSectionsFromMetadata(ctx context.Context) []systemprompt.Section {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	v := md.Get(metadataSystemPromptSectionsKey)
	if len(v) == 0 {
		return nil
	}
	raw := strings.TrimSpace(v[0])
	if raw == "" {
		return nil
	}
	payload := []byte(raw)
	if strings.HasPrefix(raw, "b64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, "b64:"))
		if err != nil {
			debugLogf("structured system prompt metadata base64 decode failed: %v", err)
			return nil
		}
		payload = decoded
	}
	var wire []wireSystemPromptSection
	if err := json.Unmarshal(payload, &wire); err != nil {
		debugLogf("structured system prompt metadata parse failed: %v", err)
		return nil
	}
	items := make([]systemprompt.Section, 0, len(wire))
	for _, s := range wire {
		items = append(items, systemprompt.Section{
			ID:      s.ID,
			Content: s.Content,
			Tags:    s.Tags,
		})
	}
	return normalizeSystemPromptSections(items)
}

func systemPromptSectionsFromRequest(ctx context.Context, req *llmv1.ChatStreamRequest, sessionDefault []systemprompt.Section) ([]systemprompt.Section, systemPromptSectionSource) {
	if fromProto := systemPromptSectionsFromProto(req); len(fromProto) > 0 {
		return fromProto, systemPromptSectionSourceProto
	}
	if fromMD := systemPromptSectionsFromMetadata(ctx); len(fromMD) > 0 {
		return fromMD, systemPromptSectionSourceMetadata
	}
	// Session-level default (set once via EnsureSession, see internal/sessionmanager)
	// — the last-resort fallback so a caller does not have to resend the system
	// prompt on every ChatStreamRequest just to keep it applied.
	if len(sessionDefault) > 0 {
		return sessionDefault, systemPromptSectionSourceSession
	}
	return nil, ""
}

func sectionIDs(sections []systemprompt.Section) []string {
	out := make([]string, 0, len(sections))
	for _, s := range sections {
		if id := strings.TrimSpace(s.ID); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func hasChatMarkdownSection(sections []systemprompt.Section) bool {
	for _, s := range sections {
		if strings.TrimSpace(s.ID) == "provider.chatMarkdown" {
			return true
		}
	}
	return false
}

func mdcFileNames(files map[string]string) []string {
	if len(files) == 0 {
		return nil
	}
	out := make([]string, 0, len(files))
	for name := range files {
		if n := strings.TrimSpace(name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func replaceSystemMessageContent(msgs []*llmv1.Message, promptBody string) []*llmv1.Message {
	body := strings.TrimSpace(promptBody)
	if body == "" {
		return msgs
	}
	out := append([]*llmv1.Message(nil), msgs...)
	for i, m := range out {
		if m != nil && strings.TrimSpace(m.GetRole()) == "system" {
			clone := proto.Clone(m).(*llmv1.Message)
			clone.Content = body
			out[i] = clone
			return out
		}
	}
	return append([]*llmv1.Message{{
		Role:    "system",
		Content: body,
		Id:      "system",
	}}, out...)
}

type appliedSystemPrompt struct {
	Messages    []*llmv1.Message
	Fingerprint string
	MDCFiles    map[string]string
}

func debugLogSystemPromptInputSections(providerID string, sections []systemprompt.Section) {
	for i, s := range sections {
		debugLogf(
			"ChatStream system prompt section provider=%q index=%d/%d id=%q content=%q",
			providerID,
			i+1,
			len(sections),
			s.ID,
			truncateRunes(strings.TrimSpace(s.Content), debugMaxSystemPromptBytes),
		)
	}
}

func debugLogSystemPromptRendered(providerID string, rendered systemprompt.RenderResult) {
	debugLogf(
		"ChatStream system prompt rendered provider=%q prompt_body=%q",
		providerID,
		truncateRunes(rendered.PromptBody, debugMaxSystemPromptBytes),
	)
	logical := strings.TrimSpace(rendered.LogicalBody)
	if logical != "" && logical != rendered.PromptBody {
		debugLogf(
			"ChatStream system prompt logical_body provider=%q content=%q",
			providerID,
			truncateRunes(logical, debugMaxSystemPromptBytes),
		)
	}
	if names := mdcFileNames(rendered.MDCFiles); len(names) > 0 {
		sort.Strings(names)
		for _, name := range names {
			debugLogf(
				"ChatStream system prompt mdc provider=%q file=%q content=%q",
				providerID,
				name,
				truncateRunes(rendered.MDCFiles[name], debugMaxSystemPromptBytes),
			)
		}
	}
	if append := strings.TrimSpace(rendered.ClaudeAppend); append != "" {
		debugLogf(
			"ChatStream system prompt claude_append provider=%q content=%q",
			providerID,
			truncateRunes(append, debugMaxSystemPromptBytes),
		)
	}
}

func debugLogLegacySystemPromptBody(providerID string, messages []*llmv1.Message) {
	for _, m := range messages {
		if m == nil || strings.TrimSpace(m.GetRole()) != "system" {
			continue
		}
		body := strings.TrimSpace(m.GetContent())
		if body == "" {
			debugLogf("ChatStream system prompt rendered provider=%q prompt_body=<empty>", providerID)
		} else {
			debugLogf(
				"ChatStream system prompt rendered provider=%q prompt_body=%q",
				providerID,
				truncateRunes(body, debugMaxSystemPromptBytes),
			)
		}
		return
	}
	debugLogf("ChatStream system prompt rendered provider=%q prompt_body=<no system message>", providerID)
}

// sessionDefaultSystemPromptSections returns the session's persisted default
// structured system prompt (set once via EnsureSession, see
// internal/sessionmanager.SessionRuntime.SystemPromptSections), converted to the
// provider-neutral systemprompt.Section shape applyStructuredSystemPrompt
// expects. Empty (nil, not an error) when the agent isn't loaded or has no
// persisted default — ChatStream falls back to legacy flat-prompt behavior
// exactly as it did before session-level defaults existed.
func (s *LlmServer) sessionDefaultSystemPromptSections(ctx context.Context, agentID string) []systemprompt.Section {
	if s == nil || s.SessionRuntimes == nil {
		return nil
	}
	ma, ok := s.runtimeFor(ctx, strings.TrimSpace(agentID))
	if !ok || len(ma.SystemPromptSections) == 0 {
		return nil
	}
	out := make([]systemprompt.Section, 0, len(ma.SystemPromptSections))
	for _, sec := range ma.SystemPromptSections {
		if sec == nil {
			continue
		}
		out = append(out, systemprompt.Section{ID: sec.GetId(), Content: sec.GetContent(), Tags: sec.GetTags()})
	}
	return normalizeSystemPromptSections(out)
}

func applyStructuredSystemPrompt(
	ctx context.Context,
	req *llmv1.ChatStreamRequest,
	messages []*llmv1.Message,
	providerID string,
	providerKind providerkind.Kind,
	sessionDefault []systemprompt.Section,
) (context.Context, appliedSystemPrompt) {
	sections, source := systemPromptSectionsFromRequest(ctx, req, sessionDefault)
	if len(sections) == 0 {
		debugLogf(
			"ChatStream system prompt delivery provider=%q delivery=legacy_flat_prompt section_count=0 section_ids=[] has_chat_markdown=false (no sections on proto or metadata)",
			providerID,
		)
		debugLogLegacySystemPromptBody(providerID, messages)
		return ctx, appliedSystemPrompt{Messages: messages}
	}
	// llmd owns provider-specific isolation content so clients stay provider-agnostic.
	sections = systemprompt.EnsureProviderIsolationSection(sections, providerID, providerKind.String())
	overlay := systemprompt.OverlayForProvider(providerID, providerKind.String())
	rendered := systemprompt.RenderSystemPrompt(sections, overlay)
	ctx = systemprompt.WithRenderedMDCFiles(ctx, rendered.MDCFiles)
	ctx = systemprompt.WithRenderedClaudeAppend(ctx, rendered.ClaudeAppend)
	applied := appliedSystemPrompt{
		Messages:    replaceSystemMessageContent(messages, rendered.PromptBody),
		Fingerprint: rendered.Fingerprint.Combined,
		MDCFiles:    rendered.MDCFiles,
	}
	debugLogf(
		"ChatStream system prompt delivery provider=%q delivery=structured_sections source=%s section_count=%d section_ids=%v has_chat_markdown=%t overlay=%t prompt_bytes=%d logical_bytes=%d mdc_files=%v fingerprint=%s",
		providerID,
		source,
		len(sections),
		sectionIDs(sections),
		hasChatMarkdownSection(sections),
		overlay != nil,
		len(rendered.PromptBody),
		len(rendered.LogicalBody),
		mdcFileNames(rendered.MDCFiles),
		truncateRunes(applied.Fingerprint, 16),
	)
	debugLogSystemPromptInputSections(providerID, sections)
	debugLogSystemPromptRendered(providerID, rendered)
	return ctx, applied
}
