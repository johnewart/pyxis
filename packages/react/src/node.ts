/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * @pyxis-llm/react/node — the Node-capable half of this package: anything that
 * needs @pyxis-llm/client (and therefore @grpc/grpc-js). Import from here only in
 * a process that can actually dial gRPC; see useLlmdChatDriver's docs for
 * the split rationale and the pattern for hosts that can't.
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

export type { ProviderAuthCommandState } from './useProviderAuthCommand.js';
export { useProviderAuthCommand } from './useProviderAuthCommand.js';
