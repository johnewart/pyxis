# lexstore

`lexstore` is a pure-Go lexical search index stored in pebble, with no embeddings, no model and no cgo. Two retrievers are fused by reciprocal rank:

- **BM25** over identifier-aware terms. `HTTPServer` yields `httpserver`, `http` and `server`; `embed_pull` yields `embed_pull`, `embed` and `pull`.
- **Name trigrams** over the names a chunk *defines* or *answers to*, so a partial or misspelled name still finds its definition.

It backs llmd's knowledge service (`internal/knowledge/store`).

## Use

```go
s, err := lexstore.Open("notes.pebble", lexstore.Options{ /* plug-ins, all optional */ })
defer s.Close()

s.UpsertDocuments(ctx, "owlbert", []lexstore.DocumentIn{
    {LogicalDocumentID: "guide#intro", Name: "guide.md", Content: "..."},
}, lexstore.UpsertOptions{})

hits, err := s.Query(ctx, "owlbert", "how does owlbert nest", lexstore.QueryFilter{}, lexstore.QueryOptions{Limit: 10})
```

## Data model

- **Space:** an opaque partition key that you choose, such as a tenant, a project or a user. Queries never cross spaces.
- **Source:** a collection within a space, such as a repository or a notebook. `""` is the primary source.
- **Partition:** `""` is live. A *derived* partition, registered with `DeclareOverlay`, holds only the files it re-indexed and reads every other file through its base.
- **Logical document:** a document is split into chunks. `FileOf(logicalID)` groups documents into files for file-level deletes, counts and partition ownership.

## Plug-ins (`Options`)

| seam | what it decides | default |
|---|---|---|
| `Chunker` | how a document splits into indexed chunks | `CharWindows(1024, 0)`; per write: `UpsertOptions.Chunker`, or `PreChunked` for one chunk per document |
| `Namer` | names a chunk **defines** (BM25-boosted and trigram-indexed) and **answers to** (trigram-indexed only) | `DefaultNamer`: defines `Metadata["symbolName"]`; answers to the logical id after `::`, the file's base name and the base of `Name` |
| `Reranker` | reorders hits when a query sets `Rerank` | none; `TermOverlapReranker` is provided |
| `FileOf` | the file a logical id belongs to | `FileOfLogicalID`, where `path::symbol` belongs to `path` |
| `Observer` | timings and volumes for metrics | none |

Examples:
- `examples/embed/main.go` plugs in markdown notes: chunks split at headings, and each chunk defines its heading.
- `bramble/knowledge/store` plugs in code: AST chunks and the symbol-aware codesearch reranker.

## Hosting through llmd

`llmdhost.Config.KnowledgeOptions` passes these plug-ins to llmd's knowledge index.

You can reach the index in two ways:
- **In process:** `Host.KnowledgeStore()` returns the `*lexstore.Store`. The KnowledgeService RPCs use the space `tenant + "\x1f" + scope`, so pick another space for direct use, or the same one to share the data.
- **Over gRPC:** through `KnowledgeService`.
