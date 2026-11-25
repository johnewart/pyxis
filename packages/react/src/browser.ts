/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * @pyxis-llm/react/browser — the browser-safe counterpart of ./node.
 *
 * Both entry points now export the SAME hooks over the SAME client type:
 * @pyxis-llm/client serves both transports, and which one you get is decided by
 * which binding you construct the client with ('@pyxis-llm/client/web' here,
 * '@pyxis-llm/client/node' there), not by which hook you import. These two modules
 * are kept only so the import site still documents the host's capability, and
 * because ./node additionally exports useProviderAuthCommand — that one really
 * is Node-only, since its RPC is bidi-streaming and gRPC-Web cannot carry bidi
 * calls at all.
 */

export type {
  ChatStreamCapableClient,
  LlmdChatDriver,
  LlmdChatDriverOptions,
  LlmdChatDriverConfig,
  OutgoingAttachment,
  QueueDrainCapableClient,
  SessionEventsSubscribableClient,
  ToolConversationEvent,
} from './useLlmdChatDriver.js';
export { useLlmdChatDriver } from './useLlmdChatDriver.js';

export type { LlmdModelCatalog } from './useLlmdModelCatalog.js';
export { useLlmdModelCatalog } from './useLlmdModelCatalog.js';

export type { LlmdSessionModel, LlmdSessionModelSelection } from './useLlmdSessionModel.js';
export { useLlmdSessionModel } from './useLlmdSessionModel.js';
