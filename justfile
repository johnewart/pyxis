# Developer recipes for llmd: build, test, npm package versioning.
# Requires: Node + pnpm (corepack enable), Go + protoc, `just` (https://just.systems).

repo := justfile_directory()

default:
	@just --list

# Builds packages/{client,core,react}/dist so consumers can link them. One
# install of the pnpm workspace, then dependency order: client -> core -> react.
dist:
	pnpm install
	pnpm --filter @pyxis-llm/client run build
	pnpm --filter @pyxis-llm/core run build
	pnpm --filter @pyxis-llm/react run build

# Keeps packages/{client,core,react}/dist current while editing (tsc --watch +
# proto/vendor regeneration), so `file:`-linked consumers pick up changes
# without re-running `dist`. Run `dist` once first on a fresh clone.
watch:
	node scripts/dev-watch.mjs

dev:
	go build -C cmd/llmd -o ../../llmd .

# Standard unit test run.
test:
	go test ./...

# Integration tests are gated behind `-tags=integration` and are NOT run by
# `go test ./...`. Most spawn the real provider CLIs and make live API calls, so
# each suite self-skips when its CLI/credentials are absent.
#   claude on PATH (or CLAUDE_CLI_PATH) -> claude suites
#   gemini on PATH (or GEMINI_CLI_PATH) -> gemini suites
# The embeddings suite needs no CLI, but it DOES need the MiniLM ONNX model and
# the pure-tokenizers shared library. First run downloads them (~90 s); after
# that they are cached under LLMD_EMBEDDING_CACHE_DIR (default
# ~/.cache/llmd/embeddings). It is tagged out of `go test ./...` because
# that cold download raced itself across parallel packages and killed the test
# binary with SIGBUS inside dlopen — an uncatchable signal, so the suites' own
# t.Skipf guards could not contain it.
# `-count=1` disables the test cache; `-v` streams progress so a hung/runaway CLI
# is visible immediately.

# Run all integration suites.
integration: integration-claude integration-gemini integration-embeddings

# Claude Code CLI integration tests (needs claude on PATH or CLAUDE_CLI_PATH).
integration-claude:
	go test -tags=integration -count=1 -v ./integration/claudecode/... ./internal/llm/claudecode/...

# Gemini CLI integration tests (needs gemini on PATH or GEMINI_CLI_PATH).
integration-gemini:
	go test -tags=integration -count=1 -v ./integration/gemini/... ./internal/llm/gemini/...

# Knowledge store suites, no CLI needed. `-p 1` runs packages one at a time.
integration-embeddings:
	go test -tags=integration -p 1 -count=1 -v ./internal/knowledge/store/...

# Every `-tags=integration` package in the module.
integration-all:
	go test -tags=integration -count=1 -v ./...

# npm package versioning (packages/client, packages/core, packages/react).
# See scripts/set-npm-versions.mjs for the full rules; short version:
#   npm-version-dev     -> fresh 9999999.<unix-seconds>.0 every run. The
#                           9999999 major always outranks any published
#                           version (local links/installs win); the
#                           timestamp makes each run unique so republishing
#                           never collides with the last one. Never publish
#                           this to the real registry.
#   npm-version-release TAG=v1.2.3
#                        -> sets that released version everywhere, in lockstep.
npm-version-dev:
	node scripts/set-npm-versions.mjs --dev

npm-version-release TAG:
	node scripts/set-npm-versions.mjs --tag {{TAG}}

# Fails if any @pyxis-llm/* package.json is on a dev version (9999999.*) — i.e.
# `npm-version-dev` ran and never got reverted. Run before
# committing/publishing a real release; `install-git-hooks` wires it in
# automatically.
npm-check-versions:
	node scripts/check-npm-dev-version.mjs

# One-time setup: installs a pre-commit hook that runs npm-check-versions.
install-git-hooks:
	cp scripts/git-hooks/pre-commit .git/hooks/pre-commit
	chmod +x .git/hooks/pre-commit
	@echo "Installed pre-commit hook (blocks committing the npm dev sentinel version)."


