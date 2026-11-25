import { describe, expect, it } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { LlmService } from '../src/gen/llmv1/llm_pb.js';
import { compactLlmd, deleteLlmdSessions, getLlmdStorageStats } from '../src/storage.js';

describe('storage RPC wrappers', () => {
  it('getLlmdStorageStats sends the limit and decodes the response', async () => {
    let limit = -1;
    const transport = createRouterTransport(({ service }) => {
      service(LlmService, {
        getStorageStats: (req) => {
          limit = req.largestSessionsLimit;
          return {
            databaseBytes: 100n,
            freeBytes: 10n,
            tables: [{ name: 't', bytes: 5n }],
            sessionCount: 2n,
            largestSessions: [
              { sessionId: 's1', title: 'T', chatSurface: 'build', messageCount: 3n, bytes: 50n, lastMessageUnixMs: 1000n },
              { sessionId: 's2', lastMessageUnixMs: 0n },
            ],
          };
        },
      });
    });
    const out = await getLlmdStorageStats({ transport, tenantId: 't', largestSessionsLimit: 5 });
    expect(limit).toBe(5);
    expect(out.databaseBytes).toBe(100);
    expect(out.freeBytes).toBe(10);
    expect(out.tables).toEqual([{ name: 't', bytes: 5 }]);
    expect(out.sessionCount).toBe(2);
    expect(out.largestSessions[0].lastMessageAt).toEqual(new Date(1000));
    expect(out.largestSessions[1].lastMessageAt).toBeNull();
  });

  it('deleteLlmdSessions sends ids and returns the count', async () => {
    let ids: string[] = [];
    const transport = createRouterTransport(({ service }) => {
      service(LlmService, {
        deleteSessions: (req) => {
          ids = req.sessionIds;
          return { deleted: 2 };
        },
      });
    });
    const n = await deleteLlmdSessions({ transport, tenantId: 't', sessionIds: ['a', 'b'] });
    expect(ids).toEqual(['a', 'b']);
    expect(n).toBe(2);
  });

  it('compactLlmd decodes the result', async () => {
    const transport = createRouterTransport(({ service }) => {
      service(LlmService, {
        compact: () => ({ runId: 'r', bytesBefore: 200n, bytesAfter: 50n, eventsRemoved: 7n }),
      });
    });
    const out = await compactLlmd({ transport, tenantId: 't' });
    expect(out).toEqual({ runId: 'r', bytesBefore: 200, bytesAfter: 50, eventsRemoved: 7 });
  });
});
