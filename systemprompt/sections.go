package systemprompt

// Static section bodies owned by llmd are PROVIDER-SPECIFIC ONLY. Shared,
// provider-neutral conversation bodies (e.g. conversation.toolUse) are owned by
// the client and arrive as structured sections on the request; llmd never keeps
// its own copy of them. llmd's job is to look like a single LLM implementation:
// inject the provider isolation body (see overlay.go / EnsureProviderIsolationSection)
// and stitch sections with delivery overlays (see render.go).
//
// These bodies say only what is true of ANY llmd run, independent of who is
// embedding llmd. llmd has no workspace model of its own: a session is reached
// over gRPC and its tools are whatever the host registered. So the isolation
// body tells the model it is headless and that llmd's tool surface is the only
// one — never anything about a particular host's layout. Policy beyond that
// belongs to the client and must arrive over the wire.

// headlessPreamble is the host-independent rule shared by every provider
// isolation body: the agent's execution location is unknowable AND irrelevant,
// so probing for it is always wrong regardless of who is embedding llmd.
const headlessPreamble = `- You are a headless agent: you do not know where you are running, and it does not matter. Never probe, infer, or report your host, working directory, or filesystem layout.
- Do not use your own built-in tools to read files, list directories, search the tree, or run commands. The tools llmd exposes are the only way to reach project state; if one fails, say so rather than reaching around it.`

// WorkspaceIsolationSection is the provider-specific workspace.isolation body for Cursor runs.
const WorkspaceIsolationSection = `LLMD + Cursor
` + headlessPreamble + `
- Cursor's built-ins for files, terminal, search, and directory listing are off. Their absence is expected and is never evidence that llmd's tools are missing or that the project is unavailable.
- Web search/browser: Cursor's own tools are fine when available.`

// ClaudeIsolationSection is the provider-specific workspace.isolation body for Claude Code runs.
const ClaudeIsolationSection = `LLMD + Claude Code
` + headlessPreamble + `
- The native Claude tools (Read/Write/Bash/Glob/Grep) are NOT available for project work in this session. Their absence is expected.
- Web search only if exposed in your llmd tool list.`
