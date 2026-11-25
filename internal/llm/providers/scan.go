package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"

	bedrockllm "github.com/johnewart/pyxis/internal/llm/bedrock"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/llmdlog"
)

const anthropicVersionHeader = "2023-06-01"

// debugEnabled reports whether verbose diagnostics logging is on (LLMD_DEBUG=1).
func debugEnabled() bool {
	return llmdlog.DebugEnabled()
}

var cursorCLIPrintAnsi = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`)

func (e *Entry) refreshModels(ctx context.Context) ([]CatalogModel, error) {
	if e == nil {
		return nil, fmt.Errorf("nil entry")
	}
	switch e.Kind {
	case providerkind.Ollama:
		return scanOllama(ctx, e.ID, e.ollamaBase)
	case providerkind.OpenAI:
		return scanOpenAI(ctx, e.ID, e.openAIBase, e.openAIKey)
	case providerkind.Anthropic:
		return scanAnthropic(ctx, e.ID, e.anthropicBase, e.anthropicKey, e.DefaultModel)
	case providerkind.CursorCLI:
		return scanCursorCLI(ctx, e.ID, e.cursorBinary, e.cursorCwd, e.cursorAPIKey, e.DefaultModel)
	case providerkind.ClaudeCodeV2:
		return scanClaudeCodeV2(e.ID, e.DefaultModel, e.claudeProbeOptions())
	case providerkind.Bedrock:
		bc, ok := e.Backend.(*bedrockllm.Client)
		if !ok || bc == nil {
			return nil, fmt.Errorf("bedrock backend not initialized")
		}
		return scanBedrock(ctx, e.ID, bc.ControlClient(), e.DefaultModel)
	case providerkind.Gemini:
		return scanGemini(ctx, e.ID, e.geminiBase, e.geminiKey, e.DefaultModel)
	case providerkind.OpenRouter:
		return scanOpenAI(ctx, e.ID, e.openAIBase, e.openAIKey)
	default:
		return nil, fmt.Errorf("unsupported provider kind")
	}
}

func scanBedrock(ctx context.Context, providerID string, ctrl *bedrock.Client, fallbackModel string) ([]CatalogModel, error) {
	if ctrl == nil {
		return nil, fmt.Errorf("nil bedrock control client")
	}
	// List without output-modality filters so embedding models (e.g.
	// amazon.titan-embed-text-v2) appear alongside chat models.
	page, err := ctrl.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{})
	if err != nil {
		if m := strings.TrimSpace(fallbackModel); m != "" {
			return []CatalogModel{{ProviderID: providerID, ModelID: m, Display: m}}, nil
		}
		return nil, fmt.Errorf("bedrock list foundation models: %w", err)
	}
	out := mergeBedrockFoundationModels(providerID, [][]btypes.FoundationModelSummary{page.ModelSummaries})
	if len(out) == 0 {
		if m := strings.TrimSpace(fallbackModel); m != "" {
			return []CatalogModel{{ProviderID: providerID, ModelID: m, Display: m}}, nil
		}
	}
	return out, nil
}

func mergeBedrockFoundationModels(providerID string, pages [][]btypes.FoundationModelSummary) []CatalogModel {
	seen := make(map[string]struct{})
	var out []CatalogModel
	for _, page := range pages {
		for _, fm := range page {
			mid := aws.ToString(fm.ModelId)
			if mid == "" {
				continue
			}
			if _, ok := seen[mid]; ok {
				continue
			}
			seen[mid] = struct{}{}
			display := mid
			if fm.ModelName != nil {
				if n := strings.TrimSpace(*fm.ModelName); n != "" {
					display = n
				}
			}
			out = append(out, CatalogModel{ProviderID: providerID, ModelID: mid, Display: display})
		}
	}
	return out
}

// ollamaNativeBase returns the configured Ollama base URL verbatim, minus
// surrounding space and a trailing slash. The URL is used as given; we do not
// rewrite path suffixes such as "/v1".
func ollamaNativeBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "http://127.0.0.1:11434"
	}
	return base
}

func scanOllama(ctx context.Context, providerID, base string) ([]CatalogModel, error) {
	base = ollamaNativeBase(base)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ollama list models: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(body.Models))
	for _, m := range body.Models {
		id := strings.TrimSpace(m.Name)
		if id == "" {
			continue
		}
		out = append(out, CatalogModel{ProviderID: providerID, ModelID: id, Display: id})
	}
	return out, nil
}

func scanOpenAI(ctx context.Context, providerID, base, apiKey string) ([]CatalogModel, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if !strings.HasSuffix(base, "/v1") {
		base = base + "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai list models: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(body.Data))
	for _, m := range body.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		out = append(out, CatalogModel{ProviderID: providerID, ModelID: id, Display: id})
	}
	return out, nil
}

func scanAnthropic(ctx context.Context, providerID, base, apiKey, fallbackModel string) ([]CatalogModel, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "https://api.anthropic.com"
	}
	base = strings.TrimRight(base, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", strings.TrimSpace(apiKey))
	req.Header.Set("anthropic-version", anthropicVersionHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A non-2xx response means the request itself failed (bad/expired API key,
		// wrong base URL, revoked access, etc.). Surface the error instead of silently
		// masking it behind default_model — doing so made the whole catalog collapse to
		// a single (default) model and hid the real problem.
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		body := strings.TrimSpace(string(b))
		if debugEnabled() {
			llmdlog.Stdout("providers", llmdlog.LevelDebug,
				"anthropic list models failed: provider=%s status=%s body=%s", providerID, resp.Status, body)
		}
		return nil, fmt.Errorf("anthropic list models: %s: %s", resp.Status, body)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if d, ok := raw["data"]; ok && len(d) > 0 {
		_ = json.Unmarshal(d, &items)
	}
	if len(items) == 0 {
		if d, ok := raw["models"]; ok && len(d) > 0 {
			_ = json.Unmarshal(d, &items)
		}
	}
	out := make([]CatalogModel, 0, len(items))
	for _, it := range items {
		var obj map[string]any
		if err := json.Unmarshal(it, &obj); err != nil {
			continue
		}
		id := ""
		if v, ok := obj["id"].(string); ok {
			id = strings.TrimSpace(v)
		}
		if id == "" {
			continue
		}
		display := id
		if v, ok := obj["display_name"].(string); ok && strings.TrimSpace(v) != "" {
			display = strings.TrimSpace(v)
		}
		out = append(out, CatalogModel{ProviderID: providerID, ModelID: id, Display: display})
	}
	if len(out) == 0 {
		// A 2xx with no parseable models is also a failure we shouldn't paper over.
		return nil, fmt.Errorf("anthropic list models: response contained no models")
	}
	return out, nil
}

func scanCursorCLI(ctx context.Context, providerID, binary, cwd, apiKey, fallbackModel string) ([]CatalogModel, error) {
	bin := strings.TrimSpace(binary)
	if bin == "" {
		bin = strings.TrimSpace(os.Getenv("CURSOR_AGENT_PATH"))
	}
	if bin == "" {
		bin = "agent"
	}
	var cmdPath string
	var err error
	if filepath.Base(bin) == bin {
		cmdPath, err = exec.LookPath(bin)
	} else {
		cmdPath, err = filepath.Abs(bin)
	}
	if err != nil {
		return cursorCLICatalogFallback(providerID, fallbackModel)
	}
	cmd := exec.CommandContext(ctx, cmdPath, "--list-models")
	if cd := strings.TrimSpace(cwd); cd != "" {
		cmd.Dir = cd
	}
	cmd.Env = os.Environ()
	if k := strings.TrimSpace(apiKey); k != "" {
		cmd.Env = append(cmd.Env, "CURSOR_API_KEY="+k)
	}
	out, err := cmd.Output()
	if err != nil {
		return cursorCLICatalogFallback(providerID, fallbackModel)
	}
	text := cursorCLIPrintAnsi.ReplaceAllString(string(out), "")
	rows, ok := parseCursorCLIListModelsOutput(text)
	if !ok || len(rows) == 0 {
		return cursorCLICatalogFallback(providerID, fallbackModel)
	}
	outModels := make([]CatalogModel, 0, len(rows))
	for _, r := range rows {
		outModels = append(outModels, CatalogModel{ProviderID: providerID, ModelID: r.id, Display: r.display})
	}
	return outModels, nil
}

type cursorCLIModelRow struct {
	id, display string
}

// parseCursorCLIListModelsOutput mirrors listCursorModels in go-agents/cursor-cli-agent.ts (JSON first, then plain text).
func parseCursorCLIListModelsOutput(text string) ([]cursorCLIModelRow, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil, false
	}
	if len(t) > 0 && (t[0] == '[' || t[0] == '{') {
		if rows, ok := parseCursorCLIModelsJSON([]byte(t)); ok && len(rows) > 0 {
			return rows, true
		}
	}
	rows := parseCursorCLIModelsPlainText(t)
	return rows, len(rows) > 0
}

func parseCursorCLIModelsJSON(raw []byte) ([]cursorCLIModelRow, bool) {
	var top any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, false
	}
	switch v := top.(type) {
	case []any:
		return rowsFromCursorJSONArray(v), true
	case map[string]any:
		if arr, ok := v["models"].([]any); ok {
			return rowsFromCursorJSONArray(arr), true
		}
		if arr, ok := v["list"].([]any); ok {
			return rowsFromCursorJSONArray(arr), true
		}
	}
	return nil, false
}

func rowsFromCursorJSONArray(arr []any) []cursorCLIModelRow {
	var out []cursorCLIModelRow
	for _, el := range arr {
		switch x := el.(type) {
		case string:
			s := strings.TrimSpace(x)
			if s != "" {
				out = append(out, cursorCLIModelRow{id: s, display: s})
			}
		case map[string]any:
			id, _ := x["id"].(string)
			if strings.TrimSpace(id) == "" {
				id, _ = x["name"].(string)
			}
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			name, _ := x["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				name = id
			}
			out = append(out, cursorCLIModelRow{id: id, display: name})
		}
	}
	return out
}

func parseCursorCLIModelsPlainText(text string) []cursorCLIModelRow {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	modelLines := lines
	if len(lines) > 2 {
		modelLines = lines[2:]
	}
	var out []cursorCLIModelRow
	for _, raw := range modelLines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Tip") {
			continue
		}
		dashIdx := strings.Index(line, " -")
		id := strings.TrimSpace(line)
		display := id
		if dashIdx >= 0 {
			id = strings.TrimSpace(line[:dashIdx])
			display = strings.TrimSpace(line[dashIdx+2:])
			if display == "" {
				display = id
			}
		}
		if id == "" {
			continue
		}
		out = append(out, cursorCLIModelRow{id: id, display: display})
	}
	return out
}

func cursorCLICatalogFallback(providerID, fallbackModel string) ([]CatalogModel, error) {
	m := strings.TrimSpace(fallbackModel)
	if m == "" {
		m = "auto"
	}
	return []CatalogModel{{ProviderID: providerID, ModelID: m, Display: m}}, nil
}

func scanClaudeCodeV2(providerID, defaultModel string, probe claudecodeclillm.ProbeOptions) ([]CatalogModel, error) {
	if r := claudecodeclillm.ProbeClaudeCode(probe); !r.OK {
		msg := strings.TrimSpace(r.Error)
		if msg == "" {
			msg = claudecodeclillm.ClaudeV2ProbeUserFacingError()
		}
		details := strings.TrimSpace(r.Details)
		if details != "" {
			return nil, fmt.Errorf("%s (%s)", msg, details)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	return scanClaudeCode(providerID, defaultModel)
}

// claudeThinkingVariants are the thinking states offered per effort tier in
// the catalog: plain (thinking disabled/off) and thinking-enabled.
var claudeThinkingVariants = []string{"disabled", "enabled"}

// scanClaudeCode returns a small static catalog (Claude Code CLI has no list-models API in llmd).
// Each base model tier (sonnet, opus, ...) is expanded into one catalog row per
// --effort level, each offered with and without extended thinking (e.g.
// "opus-low-enabled" -> "Opus — Low (Thinking)"), so the model picker can
// surface effort/thinking as first-class, selectable model variants rather
// than hidden call options nobody can reach from a picker.
func scanClaudeCode(providerID, defaultModel string) ([]CatalogModel, error) {
	seen := make(map[string]struct{})
	var out []CatalogModel
	add := func(id, display string) {
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, CatalogModel{ProviderID: providerID, ModelID: id, Display: display})
	}
	if dm := strings.TrimSpace(defaultModel); dm != "" {
		add(dm, dm)
	}
	// The Claude Code CLI has no list-models API, so these tiers ARE the catalog.
	// Sourced from claude_base_models.json (shared with the TS constants).
	for _, m := range claudecodeclillm.ClaudeBaseModels() {
		if m.Alias == "" {
			continue
		}
		label := m.Label
		if label == "" {
			label = m.Alias
		}
		// Bare alias: provider/CLI default effort and thinking behavior.
		add(m.Alias, label)
		for _, effort := range claudecodeclillm.ClaudeEffortLevels() {
			for _, thinking := range claudeThinkingVariants {
				id := m.Alias + "-" + effort + "-" + thinking
				display := label + " — " + claudecodeclillm.ClaudeEffortLabel(effort)
				if thinking == "enabled" {
					display += " (Thinking)"
				}
				add(id, display)
			}
		}
	}
	return out, nil
}

func scanGemini(ctx context.Context, providerID, base, apiKey, fallbackModel string) ([]CatalogModel, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	base = strings.TrimRight(base, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1beta/models", nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("key", strings.TrimSpace(apiKey))
	req.URL.RawQuery = q.Encode()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if m := strings.TrimSpace(fallbackModel); m != "" {
			return []CatalogModel{{ProviderID: providerID, ModelID: m, Display: m}}, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("gemini list models: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var body struct {
		Models []struct {
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(body.Models))
	for _, m := range body.Models {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		id := strings.TrimPrefix(name, "models/")
		display := strings.TrimSpace(m.DisplayName)
		if display == "" {
			display = id
		}
		out = append(out, CatalogModel{ProviderID: providerID, ModelID: id, Display: display})
	}
	if len(out) == 0 && strings.TrimSpace(fallbackModel) != "" {
		m := strings.TrimSpace(fallbackModel)
		out = append(out, CatalogModel{ProviderID: providerID, ModelID: m, Display: m})
	}
	return out, nil
}
