import { libraryMessageToProto } from "./convert.js";
import { callUnary, scopeHeaders, type Transport } from "./transport.js";
import { resolveLlmServiceClient, type LlmdCallTarget } from "./callTarget.js";
import type { Message } from "./messages.js";

export type ReplaceLlmdSessionHistoryOptions = LlmdCallTarget & {
  tenantId: string;
  scopeId?: string;
  sessionId: string;
  messages: Message[];
};

export type { Transport };

/** Replace llmd persisted chat history for a session (mirrors a client transcript after delete/truncate). */
export async function replaceLlmdSessionHistory(
  opts: ReplaceLlmdSessionHistoryOptions
): Promise<void> {
  const client = resolveLlmServiceClient(opts);
  await callUnary(
    (o) =>
      client.replaceSessionHistory(
        {
          sessionId: opts.sessionId.trim(),
          messages: opts.messages.map(libraryMessageToProto),
        },
        o
      ),
    scopeHeaders(opts),
    undefined,
    { label: "replaceSessionHistory" }
  );
}
