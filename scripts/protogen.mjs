/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * `make generate` for shells that have node but no make (agent sandboxes,
 * minimal CI images). Same inputs, same outputs, same plugin versions as the
 * Makefile's `generate` target — if the two ever disagree, the Makefile wins
 * and this file is the bug.
 *
 *   node llmd/scripts/protogen.mjs      (from the repo root)
 *   node scripts/protogen.mjs           (from llmd/)
 */

import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { fileURLToPath } from 'node:url';

const llmdDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

/**
 * Keep in lockstep with LLMD_PROTOS in llmd/Makefile.
 *
 * llmd generates only the protos it owns. Some services' protos used to be in
 * this list and are not llmd's any more, which
 * made this script fail outright on "Could not make proto path relative" — the
 * Makefile stayed correct, so the two had silently diverged.
 */
const PROTOS = [
  'knowledge/v1/knowledge.proto',
  'llamacpp/v1/llamacpp.proto',
  'llmv1/llm.proto',
  'mcpproxy/v1/mcpproxy.proto',
];

/** Keep in lockstep with the proto-plugins target in llmd/Makefile. */
const PLUGINS = [
  'google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11',
  'google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1',
];

const run = (cmd, args, env) =>
  execFileSync(cmd, args, {
    cwd: llmdDir,
    stdio: 'inherit',
    env: { ...process.env, ...env },
  });

const goEnv = (key) =>
  execFileSync('go', ['env', key], { cwd: llmdDir, encoding: 'utf8' }).trim();

// protoc invokes protoc-gen-go / protoc-gen-go-grpc by looking them up on PATH,
// so the go install destination has to be prepended — it usually is not there.
const goToolBin = goEnv('GOBIN') || path.join(goEnv('GOPATH'), 'bin');

console.log('[protogen] installing protoc plugins…');
for (const plugin of PLUGINS) run('go', ['install', plugin]);

// Mirrors clean-stale-proto: a since-deleted proto whose .pb.go lingers would
// keep compiling and quietly shadow the real definition.
fs.rmSync(path.join(llmdDir, 'gen/llmv1/embedding.pb.go'), { force: true });

// Prefer the protoc bundled with @protobuf-ts (no system protoc required):
// hoisted to the pnpm workspace's root, or in the client's own node_modules.
const bundledDir = [
  path.join(llmdDir, 'node_modules/@protobuf-ts/protoc/installed'),
  path.join(llmdDir, 'packages/client/node_modules/@protobuf-ts/protoc/installed'),
].find((d) => fs.existsSync(d));
let protoc = process.env.PROTOC || 'protoc';
if (bundledDir) {
  const found = fs
    .readdirSync(bundledDir)
    .filter((d) => d.startsWith('protoc-'))
    .map((d) => path.join(bundledDir, d, 'bin/protoc'))
    .find((p) => fs.existsSync(p));
  if (found) protoc = found;
}
console.log(`[protogen] protoc: ${protoc}`);

run(
  protoc,
  [
    '-I',
    'proto',
    '--go_out=gen',
    '--go_opt=paths=source_relative',
    '--go-grpc_out=gen',
    '--go-grpc_opt=paths=source_relative',
    ...PROTOS,
  ],
  { PATH: `${goToolBin}${path.delimiter}${process.env.PATH ?? ''}` }
);

console.log('[protogen] done');
