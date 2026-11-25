/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * `@pyxis-llm/core/node` — Node-only helpers for spawning an llmd process.
 * Kept out of the main `@pyxis-llm/core` entry because they import
 * `node:child_process` and `node:readline` at load time, which breaks any
 * browser (or jsdom) bundle that imports `@pyxis-llm/core`.
 */

export type { LlmdStartOptions, LlmdExitInfo, LlmdHandle } from './processLifecycle.js';
export { LlmdStartupError, startLlmd } from './processLifecycle.js';
