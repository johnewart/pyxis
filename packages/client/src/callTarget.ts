/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * How the standalone helper functions in this package reach llmd.
 *
 * They take a Transport (built by '@pyxis-llm/client/node' or '@pyxis-llm/client/web')
 * or an already-built client, so one implementation serves both transports and
 * callers can reuse a single connection.
 */

import { createClient } from "@connectrpc/connect";
import { LlmService } from "./gen/llmv1/llm_pb.js";
import type { LlmServiceClient } from "./llm.js";
import type { Transport } from "./transport.js";

/**
 * Either a Transport to dial with, or an existing client to reuse.
 *
 * Prefer `client` when you already hold an {@link LlmdClient} — passing
 * `llmdClient.client` reuses that connection instead of opening another.
 */
export type LlmdCallTarget =
  | { transport: Transport; client?: undefined }
  | { client: LlmServiceClient; transport?: undefined };

export function resolveLlmServiceClient(target: LlmdCallTarget): LlmServiceClient {
  return target.client ?? createClient(LlmService, target.transport);
}
