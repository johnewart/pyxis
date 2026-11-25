#!/usr/bin/env node
// Clean build of one package's dist/ that touches only the files that changed.
//
//   (from packages/<name>)  node ../../scripts/build-dist.mjs
//
// Host apps link @pyxis-llm/{client,core,react} with `file:` specifiers, so their
// dev servers (Vite) serve dist/*.js straight from disk and reload the page
// whenever one of those files changes. Builds run often, from many places
// (konstruct's test and typecheck targets build these packages first), and
// almost always produce exactly what is already there. So:
//
//   1. compile into dist.next/; if tsc fails, leave dist/ alone and exit with
//      tsc's status;
//   2. move into dist/ only the files that are new or differ, new ones first
//      (a changed file may import a new one, never the other way round);
//   3. delete what the build no longer produces (removed or renamed sources),
//      so the result is still a clean build.
//
// A build that changes nothing writes nothing, so a running dev server sees no
// change and does not reload. Each file is moved with one rename, so a reader
// never sees a half-written file. The old build deleted dist/ before running
// tsc, and a reload in that window failed with e.g. "Failed to resolve import
// ./VegaChart.js".

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, rmdirSync, statSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

const pkgDir = process.cwd();
const dist = path.join(pkgDir, 'dist');
const next = path.join(pkgDir, 'dist.next');

if (!existsSync(path.join(pkgDir, 'tsconfig.json'))) {
  console.error(`[build-dist] no tsconfig.json in ${pkgDir}; run this from a package directory.`);
  process.exit(1);
}

// Leftovers from an interrupted earlier run (dist.old is from an older version of this script).
rmSync(next, { recursive: true, force: true });
rmSync(path.join(pkgDir, 'dist.old'), { recursive: true, force: true });

const tsc = createRequire(path.join(pkgDir, 'package.json')).resolve('typescript/bin/tsc');
const r = spawnSync(process.execPath, [tsc, '-p', 'tsconfig.json', '--outDir', 'dist.next'], {
  cwd: pkgDir,
  stdio: 'inherit',
});
if (r.error) {
  console.error(`[build-dist] could not run tsc: ${r.error.message}`);
  rmSync(next, { recursive: true, force: true });
  process.exit(1);
}
if (r.status !== 0) {
  console.error(`[build-dist] tsc failed (exit ${r.status ?? r.signal}); dist/ left as it was.`);
  rmSync(next, { recursive: true, force: true });
  process.exit(r.status ?? 1);
}

if (!existsSync(dist)) {
  renameSync(next, dist);
  console.log('[build-dist] dist/ created');
  process.exit(0);
}

/** Every file under dir, as paths relative to it. */
function filesUnder(dir, rel = '') {
  const out = [];
  for (const e of readdirSync(path.join(dir, rel), { withFileTypes: true })) {
    const p = rel ? path.join(rel, e.name) : e.name;
    if (e.isDirectory()) out.push(...filesUnder(dir, p));
    else out.push(p);
  }
  return out;
}

/** Whether two files have the same bytes. */
function sameBytes(a, b) {
  if (statSync(a).size !== statSync(b).size) return false;
  return readFileSync(a).equals(readFileSync(b));
}

const built = filesUnder(next);
const builtSet = new Set(built);
const added = [];
const changed = [];
for (const f of built) {
  const cur = path.join(dist, f);
  if (!existsSync(cur)) added.push(f);
  else if (!sameBytes(path.join(next, f), cur)) changed.push(f);
}
const removed = filesUnder(dist).filter((f) => !builtSet.has(f));

for (const f of [...added, ...changed]) {
  const dest = path.join(dist, f);
  mkdirSync(path.dirname(dest), { recursive: true });
  renameSync(path.join(next, f), dest);
}
for (const f of removed) rmSync(path.join(dist, f), { force: true });

/** Remove directories under dir left empty by the deletions (never dir itself). */
function pruneEmptyDirs(dir, rel = '') {
  for (const e of readdirSync(path.join(dir, rel), { withFileTypes: true })) {
    if (!e.isDirectory()) continue;
    const p = path.join(rel, e.name);
    pruneEmptyDirs(dir, p);
    if (readdirSync(path.join(dir, p)).length === 0) rmdirSync(path.join(dir, p));
  }
}
if (removed.length > 0) pruneEmptyDirs(dist);

rmSync(next, { recursive: true, force: true });

console.log(
  added.length + changed.length + removed.length === 0
    ? '[build-dist] dist/ unchanged'
    : `[build-dist] dist/: ${changed.length} changed, ${added.length} added, ${removed.length} removed`
);
