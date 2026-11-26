#!/usr/bin/env node
/**
 * Cross-platform protoc + protoc-gen-es generation for @pyxis-llm/client.
 *
 * ONE codegen for BOTH transports. protobuf-es v2's protoc-gen-es emits both
 * the message schemas and the service descriptors into `<proto>_pb.ts`, and
 * `@connectrpc/connect`'s `createClient(Service, transport)` consumes those
 * descriptors directly — so there is no separate service-stub plugin (v1's
 * protoc-gen-connect-es is gone) and, critically, no second generated surface
 * per transport. `./node` (createGrpcTransport, HTTP/2) and `./web`
 * (createConnectTransport, fetch) share these exact types.
 *
 * Keeping the four protos listed here in one place is what stops the two
 * transports from drifting.
 *
 * protoc resolution: PROTOC=/path/to/protoc if set, else a protoc on PATH
 * (the same one `make generate` uses), else @protobuf-ts/protoc, which
 * downloads protoc on first use and so needs network access.
 */
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const pkgRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const genDir = path.join(pkgRoot, 'src', 'gen');
const protoRoot = path.resolve(pkgRoot, '../../proto');
// @pyxis-llm/client is scoped to models/chat/search only, so it stays portable to
// other llmd hosts. documents/secrets are not llmd's — those generate into
// whatever composition-root client owns workspace/document data, not here.
// mcpproxy stays here: it's llmd's own tool surface (spawns/dispatches tools),
// not workspace/graph/kanban data, so it travels with the model/tool runtime.
const protoFiles = [
  path.join(protoRoot, 'llmv1', 'llm.proto'),
  path.join(protoRoot, 'mcpproxy', 'v1', 'mcpproxy.proto'),
  // knowledge (embeddings/search) and llamacpp (managed local model runtime)
  // are llmd's own — model/provider management and knowledgebase/search are
  // both in scope, same as chat/sessions/tools.
  path.join(protoRoot, 'knowledge', 'v1', 'knowledge.proto'),
  path.join(protoRoot, 'llamacpp', 'v1', 'llamacpp.proto'),
];

fs.mkdirSync(genDir, { recursive: true });

const pluginName =
  process.platform === 'win32' ? 'protoc-gen-es.cmd' : 'protoc-gen-es';

// node_modules/<rel> in this package or any folder above it, as Node resolves
// a package: in the pnpm workspace, dependencies are hoisted to its root.
function findInNodeModules(rel) {
  for (let dir = pkgRoot; ; dir = path.dirname(dir)) {
    const p = path.join(dir, 'node_modules', rel);
    if (fs.existsSync(p)) return p;
    if (path.dirname(dir) === dir) return undefined;
  }
}

const plugin = findInNodeModules(path.join('.bin', pluginName));

if (!plugin) {
  console.error(`[proto-gen] Missing protoc-gen-es plugin (${pluginName}).`);
  console.error('[proto-gen] Run pnpm install at the pyxis root first.');
  process.exit(1);
}

for (const protoFile of protoFiles) {
  if (!fs.existsSync(protoFile)) {
    console.error(`[proto-gen] Missing proto file: ${protoFile}`);
    process.exit(1);
  }
}

function findOnPath(name) {
  for (const dir of (process.env.PATH ?? '').split(path.delimiter)) {
    // npm run prepends node_modules/.bin, where @protobuf-ts/protoc installs
    // a `protoc` shim that downloads on first use; skip it to find a real one.
    if (!dir || dir.split(path.sep).includes('node_modules')) continue;
    const candidate = path.join(dir, name);
    try {
      fs.accessSync(candidate, fs.constants.X_OK);
      return candidate;
    } catch {
      // not here; keep looking
    }
  }
  return null;
}

function resolveProtocCommand() {
  if (process.env.PROTOC) {
    return { command: process.env.PROTOC, prefixArgs: [] };
  }

  const bundled = findInNodeModules(path.join('@protobuf-ts', 'protoc', 'protoc.js'));
  if (bundled) {
    return { command: process.execPath, prefixArgs: [bundled] };
  }

  console.error('[proto-gen] Missing @protobuf-ts/protoc (bundled protoc).');
  console.error('[proto-gen] Run pnpm install at the pyxis root first.');
  console.error('[proto-gen] Or set PROTOC to a protoc binary on PATH.');
  process.exit(1);
}

const protocArgs = [
  `-I${protoRoot}`,
  `--plugin=protoc-gen-es=${plugin}`,
  `--es_out=${genDir}`,
  '--es_opt=target=ts',
  ...protoFiles,
];

const { command, prefixArgs } = resolveProtocCommand();
const result = spawnSync(command, [...prefixArgs, ...protocArgs], {
  cwd: pkgRoot,
  stdio: 'inherit',
  env: process.env,
});

if (result.error) {
  console.error(`[proto-gen] Failed to run protoc: ${result.error.message}`);
  process.exit(1);
}

process.exit(result.status ?? 1);
