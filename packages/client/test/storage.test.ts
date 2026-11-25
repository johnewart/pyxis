import { describe, expect, it } from "vitest";

import { LlmdClient } from "../src/llm.js";

function harness(handlers: Record<string, (req: Record<string, unknown>) => unknown>) {
  const requests: Record<string, Array<Record<string, unknown>>> = {};
  const client = new LlmdClient({
    transport: {} as never,
    tenantId: "tenant_test",
    scopeId: "scope_test",
    sessionId: "sess_test",
  });
  const rpc: Record<string, unknown> = {};
  for (const [name, fn] of Object.entries(handlers)) {
    rpc[name] = async (req: Record<string, unknown>) => {
      (requests[name] ??= []).push(req);
      return fn(req);
    };
  }
  (client as unknown as { client: unknown }).client = rpc;
  return { client, requests };
}

describe("LlmdClient storage wrappers", () => {
  it("getStorageStats sends the limit and returns sizes and largest sessions", async () => {
    const h = harness({
      getStorageStats: () => ({
        databaseBytes: 1000n,
        freeBytes: 200n,
        sessionCount: 3,
        tables: [{ name: "llmd_chat_messages", bytes: 600n }],
        largestSessions: [
          { sessionId: "big", title: "Big", chatSurface: "chat", messageCount: 20, bytes: 500n },
        ],
      }),
    });

    const res = await h.client.getStorageStats({ largestSessionsLimit: 5 });

    expect(h.requests.getStorageStats[0].largestSessionsLimit).toBe(5);
    expect(Number(res.databaseBytes)).toBe(1000);
    expect(Number(res.freeBytes)).toBe(200);
    expect(res.tables[0].name).toBe("llmd_chat_messages");
    expect(res.largestSessions[0].sessionId).toBe("big");
  });

  it("deleteSessions sends every id", async () => {
    const h = harness({ deleteSessions: () => ({ deleted: 2 }) });

    await h.client.deleteSessions(["a", "b"]);

    expect(h.requests.deleteSessions[0].sessionIds).toEqual(["a", "b"]);
  });

  it("compact returns bytes before and after", async () => {
    const h = harness({ compact: () => ({ bytesBefore: 900n, bytesAfter: 300n, runId: "r1" }) });

    const res = await h.client.compact();

    expect(h.requests.compact).toHaveLength(1);
    expect(Number(res.bytesBefore)).toBe(900);
    expect(Number(res.bytesAfter)).toBe(300);
  });
});
