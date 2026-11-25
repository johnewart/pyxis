#!/usr/bin/env node
// Builds the llmd binary the e2e suite spawns. Idempotent; safe to re-run.
//
// cmd/llmd is its OWN Go module (separate go.mod, deliberately nested under
// llmd's import-path prefix for Go's textual "internal" visibility rule —
// see cmd/llmd/go.mod's doc comment) — it is llmd's composition root, not
// part of the root module. `go build ./cmd/llmd` from llmdRoot therefore
// fails with "main module does not contain package"; the build must run
// with cwd = cmd/llmd itself, building `.` there.
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const e2eRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const llmdRoot = path.resolve(e2eRoot, '..', '..', '..'); // llmd/packages/client/e2e -> llmd
const cmdLlmdRoot = path.join(llmdRoot, 'cmd', 'llmd');
const outPath = path.join(llmdRoot, 'llmd-e2e-bin');

const result = spawnSync('go', ['build', '-o', outPath, '.'], {
  cwd: cmdLlmdRoot,
  stdio: 'inherit',
});
if (result.status !== 0) {
  console.error('[build-llmd] go build failed');
  process.exit(result.status ?? 1);
}
console.log('[build-llmd] built', outPath);
