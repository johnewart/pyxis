/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * One llmd for the whole react e2e run. Every file's `startE2eEnv()` finds it
 * via SHARED_LLMD_ENV instead of spawning its own; isolation between files
 * comes from per-file tenants/scopes and per-file fake OpenAI servers.
 */

import { startSharedE2eLlmd } from '../../client/e2e/harness.js';

export default async function setup(): Promise<() => Promise<void>> {
  return startSharedE2eLlmd();
}
