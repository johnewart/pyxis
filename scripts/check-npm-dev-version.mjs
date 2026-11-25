#!/usr/bin/env node
// Fails if any @pyxis-llm/* package.json (or @pyxis-llm/core's optionalDependencies
// pins) is set to a dev version (9999999.<anything>) — i.e. `just
// npm-version-dev` ran and its result got committed instead of reverted
// before publishing/committing a real release.
//
// Run manually, or wire into CI/a pre-commit hook, e.g.:
//   node scripts/check-npm-dev-version.mjs

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { isDevVersion, ALL_PACKAGE_DIRS, PYPROJECT_PATH } from './npm-version-config.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '..');

let failed = false;

for (const dir of ALL_PACKAGE_DIRS) {
  const pkgPath = path.join(repoRoot, dir, 'package.json');
  const pkg = JSON.parse(readFileSync(pkgPath, 'utf8'));

  if (isDevVersion(pkg.version)) {
    console.error(`${dir}/package.json: version is a dev version (${pkg.version})`);
    failed = true;
  }

  for (const [dep, version] of Object.entries(pkg.optionalDependencies ?? {})) {
    if (isDevVersion(version)) {
      console.error(`${dir}/package.json: optionalDependencies["${dep}"] is a dev version (${version})`);
      failed = true;
    }
  }

  for (const field of ['dependencies', 'devDependencies', 'peerDependencies']) {
    for (const [dep, spec] of Object.entries(pkg[field] ?? {})) {
      if (dep.startsWith('@pyxis-llm/') && typeof spec === 'string' && spec.startsWith('file:')) {
        console.error(`${dir}/package.json: ${field}["${dep}"] is a local link (${spec}); it would publish uninstallable`);
        failed = true;
      }
    }
  }
}

const pyVersion = readFileSync(path.join(repoRoot, PYPROJECT_PATH), 'utf8').match(
  /^version\s*=\s*"([^"]*)"/m
)?.[1];
if (isDevVersion(pyVersion)) {
  console.error(`${PYPROJECT_PATH}: version is a dev version (${pyVersion})`);
  failed = true;
}

if (failed) {
  console.error(
    '\nRun `just npm-version-release TAG=<real version>` (or `node scripts/set-npm-versions.mjs <version>`) ' +
      'to restore a real version before committing/publishing.'
  );
  process.exit(1);
}

console.log('OK: no @pyxis-llm/* package.json uses a dev version.');
