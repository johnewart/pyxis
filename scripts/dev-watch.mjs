#!/usr/bin/env node
// Keeps packages/{client,core,react}/dist current while you work, so an app
// that consumes them through `file:` links (any host app)
// sees llmd edits without running `just dist` by hand.
//
//   just watch            (or: node scripts/dev-watch.mjs)
//
// What it does:
//   - proto/**.proto changes  -> re-run @pyxis-llm/client's proto:gen
//   - client shared types     -> re-run vendor-shared-types for @pyxis-llm/react
//   - everything else         -> one `tsc --watch` per package, writing into
//                                dist/ incrementally
//
// Nothing here deletes dist/, so a consumer's dev server never sees a
// half-empty package mid-build (`npm run build` gets the same guarantee by
// compiling into dist.next/ and moving in only the files that changed:
// scripts/build-dist.mjs). The flip side: a source file you delete or rename
// leaves its old .js in dist/ until the next `just dist`, which also removes
// output the sources no longer produce.
//
// Requires the workspace's node_modules (run `just dist` once on a fresh clone).

import { spawn, spawnSync } from 'node:child_process';
import { watch } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const pkg = (name) => path.join(repoRoot, 'packages', name);
// Dependency order: react's build reads core's and client's dist.
const PACKAGES = ['client', 'core', 'react'];
// Must match SOURCES in vendor-shared-types.mjs.
const VENDORED = new Set(['messages.ts', 'sessionQueue.ts', 'conversationEvents.ts']);

// Each package's tsc, found the way Node finds it from that package (the pnpm
// workspace hoists it to the pyxis root).
const tscOf = {};
for (const name of PACKAGES) {
  try {
    tscOf[name] = createRequire(path.join(pkg(name), 'package.json')).resolve('typescript/bin/tsc');
  } catch {
    console.error(`[dev-watch] no typescript for packages/${name}; run \`just dist\` once first.`);
    process.exit(1);
  }
}

function log(tag, line) {
  if (line.trim()) process.stdout.write(`[${tag}] ${line}\n`);
}

function runOnce(tag, args, cwd) {
  const r = spawnSync(process.execPath, args, { cwd, encoding: 'utf8' });
  for (const l of `${r.stdout ?? ''}${r.stderr ?? ''}`.split('\n')) log(tag, l);
  if (r.status !== 0) log(tag, `FAILED (exit ${r.status})`);
  return r.status === 0;
}

const protoGen = () => runOnce('proto', ['scripts/proto-gen.mjs'], pkg('client'));
const vendor = () => runOnce('vendor', ['scripts/vendor-shared-types.mjs', 'packages/react'], repoRoot);

/** Coalesce bursts of fs events (editors write several times per save). */
function debounced(fn, ms = 200) {
  let t;
  return () => {
    clearTimeout(t);
    t = setTimeout(fn, ms);
  };
}

// One-off steps before the compilers start, so their first pass is complete.
protoGen();
vendor();

const children = [];
for (const name of PACKAGES) {
  const tsc = tscOf[name];
  const child = spawn(
    process.execPath,
    [tsc, '-p', 'tsconfig.json', '--watch', '--preserveWatchOutput'],
    { cwd: pkg(name), stdio: ['ignore', 'pipe', 'pipe'] }
  );
  for (const stream of [child.stdout, child.stderr]) {
    let buf = '';
    stream.setEncoding('utf8');
    stream.on('data', (chunk) => {
      buf += chunk;
      const lines = buf.split('\n');
      buf = lines.pop() ?? '';
      for (const l of lines) log(name, l);
    });
  }
  child.on('exit', (code, signal) => {
    log(name, `tsc exited (${signal ?? code}); stopping`);
    shutdown(1);
  });
  children.push(child);
}

watch(path.join(repoRoot, 'proto'), { recursive: true }, (_e, file) => {
  if (file && String(file).endsWith('.proto')) debouncedProto();
});
const debouncedProto = debounced(protoGen);

const debouncedVendor = debounced(vendor);
watch(path.join(pkg('client'), 'src'), (_e, file) => {
  if (file && VENDORED.has(String(file))) debouncedVendor();
});

let stopping = false;
function shutdown(code = 0) {
  if (stopping) return;
  stopping = true;
  for (const c of children) c.kill('SIGTERM');
  process.exit(code);
}
process.on('SIGINT', () => shutdown(0));
process.on('SIGTERM', () => shutdown(0));

log('dev-watch', 'watching packages/{client,core,react} -> dist (Ctrl-C to stop)');
