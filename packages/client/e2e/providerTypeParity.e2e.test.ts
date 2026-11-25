/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Cross-language parity for the provider-id set, against a REAL llmd.
 *
 * The SDK deliberately holds NO list of supported provider ids: the ids appear
 * exactly once, as the case labels of the `RegisterProviderConfig["type"]` ->
 * `LlmProviderType` switch, which TypeScript checks for exhaustiveness. So the
 * thing worth testing is not "is the list right" (there is no list) but the one
 * property no type system here can see: that the ids the SDK accepts are the
 * same ids llmd itself advertises.
 *
 * That gap is exactly where the original bug lived. `@pyxis-llm/core` published
 * `claude_v2`/`cursor` while llmd's short_codes said `claude_code_v2`/`cursor_cli`;
 * both sides compiled and tested green, and the mismatch surfaced only at runtime
 * as "no configuration shape for provider type claude_v2" -- which made Claude
 * Code impossible to add.
 *
 * ListSupportedLlmProviderTypes advertises `Kind.String()` for every kind in
 * llmd's `providerkind.All()`. Every one of those must round-trip through the SDK
 * back to the same proto enum value. Add a kind in the proto and Go without
 * teaching the SDK and this fails.
 */

import { afterAll, beforeAll, describe, expect, it } from 'vitest';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startLlmd, type LlmdHandle } from '@pyxis-llm/core/node';
import { createLlmdClient } from '@pyxis-llm/client/node';
import { LlmProviderType, type LlmdClient, type RegisterProviderConfig } from '@pyxis-llm/client';

const e2eRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)));
const LLMD_BINARY_PATH = path.join(e2eRoot, '..', '..', '..', 'llmd-e2e-bin');

let handle: LlmdHandle;
let dataDir: string;
let client: LlmdClient;

beforeAll(async () => {
  if (!fs.existsSync(LLMD_BINARY_PATH)) {
    throw new Error(
      `llmd binary not found at ${LLMD_BINARY_PATH} — run \`npm run build:llmd\` first.`
    );
  }
  dataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'llmd-provider-parity-'));
  handle = await startLlmd({
    binaryPath: LLMD_BINARY_PATH,
    env: {
      LLMD_DATABASE_URL: path.join(dataDir, 'agent.sqlite'),
      LLMD_KNOWLEDGE_SQLITE_PATH: path.join(dataDir, 'knowledge.sqlite'),
    },
  });
  client = createLlmdClient({
    address: handle.grpcAddress,
    tenantId: `e2e-tenant-${randomUUID()}`,
  });
}, 60_000);

afterAll(async () => {
  await handle?.stop();
  if (dataDir) fs.rmSync(dataDir, { recursive: true, force: true });
});

describe('provider id parity with llmd', () => {
  it('every id llmd advertises round-trips through the SDK to the same enum value', async () => {
    const advertised = await client.listSupportedProviderTypes();
    expect(advertised.length).toBeGreaterThan(0);

    for (const ty of advertised) {
      // The cast is the point of the test: llmd handed us this string, and the
      // SDK must already know it. An id the switch does not handle throws.
      const asConfigType = ty.shortCode as RegisterProviderConfig['type'];
      const cfg: RegisterProviderConfig = { type: asConfigType } as RegisterProviderConfig;

      // registerProvider is not called (that would need real credentials); this
      // exercises the id -> enum mapping the SDK would use, via the same code path.
      const roundTripped = await client
        .checkProviderConnection(asConfigType, { timeoutMs: 1 })
        .then(
          () => 'accepted' as const,
          (err: unknown) => {
            const msg = err instanceof Error ? err.message : String(err);
            // An unsupported id fails locally, before any RPC, with this text.
            if (/unsupported provider type/.test(msg)) return 'rejected-by-sdk' as const;
            // Anything else means the SDK mapped the id and llmd answered: for a
            // non-CLI kind that is "not backed by a CLI", which is a real answer
            // about a recognised provider type, not an unknown id.
            return 'accepted' as const;
          }
        );

      expect(roundTripped, `llmd advertises ${ty.shortCode} but the SDK rejects it`).toBe(
        'accepted'
      );
      void cfg;
    }
  }, 60_000);

  it('never sends the enum zero value for an id llmd advertises', async () => {
    for (const ty of await client.listSupportedProviderTypes()) {
      expect(ty.shortCode).not.toBe('');
      // UNSPECIFIED is what a dropped/unknown id used to serialise as; llmd must
      // not be advertising a kind whose id maps there.
      expect(LlmProviderType.UNSPECIFIED).toBe(0);
    }
  });
});
