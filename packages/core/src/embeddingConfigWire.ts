/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/** Shape matching llmd's knowledgebase EmbeddingConfig proto (ts-proto after regen). */
export type EmbeddingConfigWire = {
  providerId?: string;
  model: string;
  dimensions: number;
  indexModelId: string;
};
