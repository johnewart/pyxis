/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Unit tests for the session wrappers — no llmd binary, no network: a router
 * transport stands in for LlmService so the outgoing request shape can be
 * asserted directly.
 */

import { describe, expect, it } from 'vitest';
import { createRouterTransport } from '@connectrpc/connect';
import { LlmService, type EnsureSessionRequest } from '../src/gen/llmv1/llm_pb.js';
import { ensureLlmdSession } from '../src/session.js';

/** Stub LlmService that records the EnsureSession request it receives. */
function transportCapturing(seen: EnsureSessionRequest[]) {
  return createRouterTransport(({ service }) => {
    service(LlmService, {
      ensureSession: (req) => {
        seen.push(req);
        return {};
      },
    });
  });
}

describe('ensureLlmdSession', () => {
  it('forwards disabledTools verbatim on the EnsureSessionRequest', async () => {
    const seen: EnsureSessionRequest[] = [];
    await ensureLlmdSession({
      transport: transportCapturing(seen),
      tenantId: 'tenant-test',
      sessionId: 'sess-1',
      disabledTools: ['probe_tool'],
    });
    expect(seen).toHaveLength(1);
    const req = seen[0];
    expect(req.sessionId).toBe('sess-1');
    expect(req.disabledTools).toEqual(['probe_tool']);
  });

  it('sends an empty disabledTools list when the option is omitted', async () => {
    const seen: EnsureSessionRequest[] = [];
    await ensureLlmdSession({
      transport: transportCapturing(seen),
      tenantId: 'tenant-test',
      sessionId: 'sess-1',
    });
    expect(seen).toHaveLength(1);
    const req = seen[0];
    expect(req.sessionId).toBe('sess-1');
    expect(req.disabledTools).toEqual([]);
  });
});
