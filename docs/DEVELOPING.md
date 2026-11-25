# Pyxis developer notes

## SQLite (CGO)

All application SQLite access in this module goes through `gorm.io/driver/sqlite`, which uses `github.com/mattn/go-sqlite3` (CGO). Do not introduce `glebarez/sqlite`, `modernc.org/sqlite`, or other pure-Go drivers for Pyxis application databases.

**Build requirements**

- Set `CGO_ENABLED=1`.
- A C toolchain must be available for each target OS/architecture (including Linux containers).

**Windows (native `llmd-windows-amd64.exe`)**

- Use **MSYS2 MinGW-w64** (`x86_64-w64-mingw32-gcc`) — the same toolchain GitHub Actions adds to `PATH` for Pyxis releases.
- Install SQLite headers + import libraries for MinGW, for example:

  `C:\msys64\usr\bin\bash.exe -lc "pacman -S --needed --noconfirm mingw-w64-x86_64-sqlite"`

- From `llmd/`, run `./build-windows.ps1` (sets `CGO_CFLAGS` / `CGO_LDFLAGS` to `C:\msys64\mingw64\include` + `lib` when present).

A locally built `llmd` binary in the repo root is a git-ignored build artifact; grepping it may show unrelated module strings. For driver audits, restrict searches to `*.go` sources.

## Knowledge service

The knowledge index is a **lexical** store (`lexstore`: BM25 plus name trigrams in pebble). It has no embeddings and no model downloads, and it is kept in a directory **separate** from the main `LLMD_DATABASE_URL` database. **Pyxis always registers `knowledge.v1.KnowledgeService`.** Every document is keyed by the exact `(tenant_id, scope_id)`.

| Variable | Purpose |
|----------|---------|
| `LLMD_KNOWLEDGE_DIR` | Knowledge index directory. Defaults to `LLMD_KNOWLEDGE_SQLITE_PATH` with `.sqlite` replaced by `.pebble`, i.e. `llmd-knowledge.pebble`. |
| `LLMD_KNOWLEDGE_SQLITE_PATH` | Where the retired SQLite knowledge database lived (default `llmd-knowledge.sqlite`). A file there is **deleted** at startup. The index is derived data, and clients re-upsert. |

The RPCs still accept `embedding_model_id` and embedding-config fields for wire compatibility. They have no effect, and no model-mismatch error is returned. Hosts plug their corpus in with `llmdhost.Config.KnowledgeOptions` (chunker, namer, reranker, `FileOf`, metrics observer) and can use the index in process via `Host.KnowledgeStore()`. See `lexstore/README.md`.

## CI

GitHub Actions: `.github/workflows/go-ci.yml` runs `go test ./...`. The knowledge store needs no cgo or extension.

## Regenerating protobufs

From `llmd/` (requires `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc` on PATH):

```bash
bash scripts/regen-knowledge-proto.sh
# or without local toolchain:
bash scripts/regen-knowledge-proto-docker.sh
```

Regenerate all Go stubs:

```bash
protoc -I proto --go_out=gen --go_opt=paths=source_relative \
  --go-grpc_out=gen --go-grpc_opt=paths=source_relative \
  knowledge/v1/knowledge.proto llmv1/llm.proto
```

**Do not hand-edit** `gen/**/*.pb.go` `rawDesc` strings — that corrupts the embedded file descriptor and panics at init (`non-contiguous repeated field`).

## Conversation stream: activity headlines

Wire event `assistant_activity_snapshot` (`EvAssistantActivitySnapshot`) carries a **full snapshot** headline for the active assistant turn (`turn_id` + `headline`). It is user-facing status, not chain-of-thought. Clients should **replace** any prior headline for the same `turn_id` when a new snapshot arrives.

### Model tag convention (`<activity>`)

Streaming providers parse **literal** `<activity>...</activity>` regions from the **user-visible assistant text stream** (after `<memory>` stripping, before `<thinking>` splitting). Completed blocks emit `assistant_activity_snapshot` with the trimmed inner text; the tags and inner text are **not** forwarded as `assistant_text_delta` / final visible content (same idea as `<thinking>`).

Models may interleave multiple activity blocks (each completion emits a new snapshot). Tags may be split across chunks; an unclosed trailing `<activity>` at end-of-stream is **not** treated as protocol — the opening tag and buffered bytes pass through as literal visible assistant text (no final snapshot).

Any code path may still emit `assistant_activity_snapshot` directly for experiments or future native provider channels.

### Persistence (`activity_headline`)

`llmv1.Message` and `llmv1.AssistantReply` carry optional `activity_headline` (latest user-facing headline for that turn). During `ChatStream`, Pyxis tracks the most recent non-empty `assistant_activity_snapshot` headline and, if the final `stream_complete` reply omits the field, patches the stored assistant `Message` before writing session history (mirrors `thinking_content` + thinking deltas).

SQLite/Postgres history stores assistant turns as multiple `llmd_chat_messages` rows (`kind` includes `activity` alongside `thinking`, `assistant`, `tool`). `GetSessionHistory` merges those rows back into a single `Message` with `activity_headline` populated for replay.
