// Package providerkind owns THE closed set of LLM provider kinds llmd supports.
//
// It is a leaf package on purpose: both internal/store (which persists a kind)
// and internal/llm/providers (which builds backends from one) import it, and
// providers already imports store, so the type cannot live in either.
//
// Why this package exists at all: a provider kind used to be a bare `string` on
// store.LlmProviderConfig, so the only way back to the closed type was to
// re-parse it. That parse grew an alias table -- "cursor" and "claude_v2" from
// an SDK that had invented its own names, plus dashed and v1 spellings -- and a
// name that matched nothing silently became the zero value, which then travelled
// as far as llmd answering "provider type LLM_PROVIDER_TYPE_UNSPECIFIED is not
// backed by a CLI". A kind is a closed enum; carry it as one.
//
// Rules for this file:
//
//   - ONE id per kind. The id is Kind.String(), and it is simultaneously the
//     `short_code` on ListSupportedLlmProviderTypes, the `type` on
//     @pyxis-llm/client's RegisterProviderConfig, and the value in the kind column.
//   - Parse accepts exactly those ids. No aliases, no case folding, no
//     trimming. Anything else is garbage and is rejected.
//   - Adding a kind means adding it here, to All(), and to the String()/Parse
//     pair. The exhaustive switches elsewhere then fail to compile or return
//     an error, which is the point.
package providerkind

// Kind identifies a registered provider implementation.
type Kind int

const (
	Unspecified Kind = iota
	OpenAI
	Ollama
	Anthropic
	CursorCLI
	ClaudeCodeV2
	Bedrock
	Gemini
	OpenRouter
)

// String is the one wire/storage id for a kind. The empty string for
// Unspecified (and any value outside the set) is deliberate: it can never be
// mistaken for a real id, and Parse rejects it.
func (k Kind) String() string {
	switch k {
	case OpenAI:
		return "openai"
	case Ollama:
		return "ollama"
	case Anthropic:
		return "anthropic"
	case CursorCLI:
		return "cursor_cli"
	case ClaudeCodeV2:
		return "claude_code_v2"
	case Bedrock:
		return "bedrock"
	case Gemini:
		return "gemini"
	case OpenRouter:
		return "openrouter"
	default:
		return ""
	}
}

// Parse resolves an id produced by String back to its Kind.
//
// Exact match against the fixed set, and nothing else: no aliases, no case
// folding, no whitespace trimming. A caller that sends anything else sent
// garbage, and gets ok=false rather than a guess or a zero value dressed up as
// a real kind.
func Parse(s string) (Kind, bool) {
	switch s {
	case "openai":
		return OpenAI, true
	case "ollama":
		return Ollama, true
	case "anthropic":
		return Anthropic, true
	case "cursor_cli":
		return CursorCLI, true
	case "claude_code_v2":
		return ClaudeCodeV2, true
	case "bedrock":
		return Bedrock, true
	case "gemini":
		return Gemini, true
	case "openrouter":
		return OpenRouter, true
	default:
		return Unspecified, false
	}
}

// Valid reports whether k is a real kind (i.e. a member of All()).
func (k Kind) Valid() bool {
	return k.String() != ""
}

// All returns every registrable provider kind in a stable, user-facing order.
func All() []Kind {
	return []Kind{
		OpenAI,
		Anthropic,
		Ollama,
		Bedrock,
		Gemini,
		OpenRouter,
		CursorCLI,
		ClaudeCodeV2,
	}
}

// StatefulProviderHistory is true when the backend keeps server-side transcript (Cursor CLI --resume).
// llmd still stores the canonical history; when ProviderSessionSync is configured on the server, ChatStream sends only
// unsynced tail slices and replays the full transcript when the system prompt fingerprint or watermark drifts.
func (k Kind) StatefulProviderHistory() bool {
	return k == CursorCLI || k == ClaudeCodeV2
}

// DisplayName is the human-readable label for an "add provider" UI.
func (k Kind) DisplayName() string {
	switch k {
	case OpenAI:
		return "OpenAI"
	case Ollama:
		return "Ollama"
	case Anthropic:
		return "Anthropic"
	case CursorCLI:
		return "Cursor CLI"
	case ClaudeCodeV2:
		return "Claude Code"
	case Bedrock:
		return "Amazon Bedrock"
	case Gemini:
		return "Google Gemini"
	case OpenRouter:
		return "OpenRouter"
	default:
		return ""
	}
}

// RequiresAPIKey reports whether registering this kind needs an API key field
// (as opposed to a local CLI binary/session or ambient cloud credentials).
func (k Kind) RequiresAPIKey() bool {
	switch k {
	case OpenAI, Anthropic, Gemini, OpenRouter:
		return true
	default:
		return false
	}
}

// IsAPIBacked reports whether this kind calls a remote HTTP API (as opposed
// to a local CLI or a local Ollama server). These are the kinds whose calls
// are wrapped with the retry decorator.
func (k Kind) IsAPIBacked() bool {
	switch k {
	case OpenAI, Anthropic, Bedrock, OpenRouter:
		return true
	}
	return false
}

// IsCLIBacked reports whether this kind shells out to a local CLI rather than
// calling an HTTP API directly.
func (k Kind) IsCLIBacked() bool {
	return k == CursorCLI || k == ClaudeCodeV2
}
