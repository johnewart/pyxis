/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Canonical built-in ONNX embedding model catalog for llmd's knowledgebase
 * (indexing, querying, retrieval). IDs and dimensions match llmd's own
 * `internal/knowledge/embed` package — this is the ONE place that
 * vocabulary is declared in TypeScript so hosts never hardcode a second copy.
 *
 * Not code-specific: despite two of the three models being code-tuned
 * (CodeBERT, Jina Code), the catalog covers any document a knowledgebase
 * embeds, not just source code. Code-only chunking concerns (e.g. AST vs.
 * file-based splitting) are a host/ingestion decision and stay out of here.
 */
export const EMBEDDING_MODEL_IDS = [
  'minilm-l6-v2',
  'codebert-base',
  'jina-embeddings-v2-base-code',
] as const;

export type EmbeddingModelId = (typeof EMBEDDING_MODEL_IDS)[number];

const ONNX_DIMENSIONS: Record<EmbeddingModelId, number> = {
  'minilm-l6-v2': 384,
  'codebert-base': 768,
  'jina-embeddings-v2-base-code': 768,
};

/** Vector dimension for a built-in ONNX embedding model. */
export function dimensionForEmbeddingModel(model: EmbeddingModelId): number {
  return ONNX_DIMENSIONS[model] ?? 384;
}

export const EMBEDDING_MODEL_OPTIONS: ReadonlyArray<{
  value: EmbeddingModelId;
  label: string;
  description: string;
}> = [
  {
    value: 'minilm-l6-v2',
    label: 'MiniLM-L6-v2',
    description: 'Fastest, ~90 MB, 384-dim — good default for most machines',
  },
  {
    value: 'codebert-base',
    label: 'CodeBERT-base',
    description: 'Code-focused, ~500 MB, 768-dim',
  },
  {
    value: 'jina-embeddings-v2-base-code',
    label: 'Jina Code v2',
    description: 'Strong code retrieval, ~320 MB FP16, 768-dim',
  },
];

/** Normalize a loosely-typed/aliased embedding-model string to a canonical id. */
export function normalizeEmbeddingModel(
  raw: string | undefined | null
): EmbeddingModelId {
  const v = String(raw ?? '')
    .trim()
    .toLowerCase();
  switch (v) {
    case '':
    case 'small':
    case 'minilm':
    case 'minilm-l6-v2':
      return 'minilm-l6-v2';
    case 'base-code':
    case 'base_code':
    case 'codebert':
    case 'codebert-base':
      return 'codebert-base';
    case 'jina':
    case 'jina-code':
    case 'jina-embeddings-v2-base-code':
      return 'jina-embeddings-v2-base-code';
    default:
      if ((EMBEDDING_MODEL_IDS as readonly string[]).includes(v)) {
        return v as EmbeddingModelId;
      }
      return 'minilm-l6-v2';
  }
}
