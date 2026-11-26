#!/usr/bin/env node
// Sets the version of every @pyxis-llm/* npm package (packages/client,
// packages/core, packages/react) to a single value. For a release, the file:
// links between them are pointed at that version too.
//
// Usage:
//   node scripts/set-npm-versions.mjs --dev
//     Sets a fresh dev version (9999999.<unix-seconds>.0) everywhere, every
//     time you run it. The 9999999 major numerically outranks any real
//     published version, so a local `npm link`/`npm install <path>` always
//     wins over the registry; the timestamp makes each run a new, unique
//     version. Never publish this to the real registry.
//
//   node scripts/set-npm-versions.mjs --tag v1.4.2
//   node scripts/set-npm-versions.mjs 1.4.2
//     Sets the given released version (leading "v" stripped) everywhere.
//
//   node scripts/set-npm-versions.mjs
//     Auto-detects: GITHUB_REF_NAME (or GITHUB_REF) if it looks like a tag,
//     else `git describe --tags --exact-match` on HEAD, else falls back to
//     --dev behavior (safe default for local/dev runs).

import { readFileSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { freshDevVersion, isDevVersion, ALL_PACKAGE_DIRS, PYPROJECT_PATH, toPep440 } from './npm-version-config.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..');

function stripLeadingV(v) {
  return v.startsWith('v') ? v.slice(1) : v;
}

function looksLikeVersion(v) {
  return /^\d+\.\d+\.\d+/.test(v);
}

function detectVersion(argv) {
  const devFlag = argv.includes('--dev');
  const tagIdx = argv.indexOf('--tag');
  if (tagIdx !== -1 && argv[tagIdx + 1]) {
    return stripLeadingV(argv[tagIdx + 1]);
  }
  const bare = argv.find((a) => !a.startsWith('--'));
  if (bare) {
    return stripLeadingV(bare);
  }
  if (devFlag) {
    return freshDevVersion();
  }

  // Auto-detect from CI tag ref.
  const ref = process.env.GITHUB_REF_NAME || process.env.GITHUB_REF || '';
  if (/^v?\d+\.\d+\.\d+/.test(ref)) {
    return stripLeadingV(ref);
  }

  // Auto-detect from an exact-match git tag on HEAD (local release cut).
  try {
    const tag = execFileSync('git', ['describe', '--tags', '--exact-match'], {
      cwd: repoRoot,
      stdio: ['ignore', 'pipe', 'ignore'],
    })
      .toString()
      .trim();
    if (tag && looksLikeVersion(stripLeadingV(tag))) {
      return stripLeadingV(tag);
    }
  } catch {
    // No exact-match tag on HEAD — fall through to dev default.
  }

  return freshDevVersion();
}

function setVersion(pkgDir, version) {
  const pkgPath = path.join(repoRoot, pkgDir, 'package.json');
  const pkg = JSON.parse(readFileSync(pkgPath, 'utf8'));
  pkg.version = version;
  // For a real release, sibling @pyxis-llm/* packages linked with workspace:*
  // (or an older file:../x) must point at the released version: a publisher
  // that does not rewrite them publishes them verbatim, which makes the
  // package uninstallable from the registry. Dev runs keep the local links.
  if (!isDevVersion(version)) {
    for (const field of ['dependencies', 'devDependencies', 'peerDependencies']) {
      const deps = pkg[field];
      if (!deps) continue;
      for (const [dep, spec] of Object.entries(deps)) {
        if (dep.startsWith('@pyxis-llm/') && typeof spec === 'string' && (spec.startsWith('file:') || spec.startsWith('workspace:'))) {
          deps[dep] = version;
        }
      }
    }
  }
  writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
  console.log(`${pkgDir}/package.json -> ${version}`);
}

function setPyprojectVersion(version) {
  const tomlPath = path.join(repoRoot, PYPROJECT_PATH);
  const text = readFileSync(tomlPath, 'utf8');
  // Replace only the version line inside the [project] table.
  const re = /(\[project\][^[]*?^version\s*=\s*)"[^"]*"/ms;
  if (!re.test(text)) {
    throw new Error(`${PYPROJECT_PATH}: no version found in [project] table`);
  }
  const pyVersion = toPep440(version);
  writeFileSync(tomlPath, text.replace(re, `$1"${pyVersion}"`));
  console.log(`${PYPROJECT_PATH} -> ${pyVersion}`);
}

function main() {
  const argv = process.argv.slice(2);
  const version = detectVersion(argv);

  if (isDevVersion(version)) {
    console.log(`Using fresh dev version ${version} (never publish this to the real registry).`);
  } else {
    console.log(`Using version ${version}.`);
  }

  for (const dir of ALL_PACKAGE_DIRS) {
    setVersion(dir, version);
  }
  setPyprojectVersion(version);
}

main();
