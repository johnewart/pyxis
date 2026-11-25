// Shared constants for scripts/set-npm-versions.mjs and
// scripts/check-npm-dev-version.mjs — keep these two scripts in sync by
// importing from here rather than duplicating the list/sentinel.

// Dev versions look like `9999999.<unix-seconds>.0`. The fixed 9999999 major
// numerically outranks any real published version (so local installs always
// win); the timestamp minor makes every run unique so republishing to a
// registry (which rejects reusing an existing version) just works — no
// manual bumping needed between local-dev iterations.
export const DEV_MAJOR = process.env.LLMD_DEV_NPM_VERSION_MAJOR || '9999999';

export function isDevVersion(version) {
  return typeof version === 'string' && version.startsWith(`${DEV_MAJOR}.`);
}

// Converts an npm semver string to PEP 440 for pyproject.toml.
//   1.4.2-rc1 / 1.4.2-rc.1 -> 1.4.2rc1, 1.4.2-alpha.1 -> 1.4.2a1,
//   1.4.2-beta.2 -> 1.4.2b2, 1.4.2-dev.3 -> 1.4.2.dev3,
//   1.4.2+build.5 -> 1.4.2+build.5 (local version).
// Dev versions (9999999.<seconds>.0) are already valid PEP 440 release
// segments and pass through unchanged, so isDevVersion still matches.
export function toPep440(version) {
  const m = /^(\d+\.\d+\.\d+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$/.exec(version);
  if (!m) {
    throw new Error(`cannot convert "${version}" to a PEP 440 version`);
  }
  const [, base, pre, build] = m;
  let out = base;
  if (pre) {
    const pm = /^([A-Za-z]+)[.-]?(\d*)$/.exec(pre);
    const num = pm ? pm[2] || '0' : '0';
    const label = pm ? pm[1].toLowerCase() : '';
    if (label === 'alpha' || label === 'a') out += `a${num}`;
    else if (label === 'beta' || label === 'b') out += `b${num}`;
    else if (label === 'rc' || label === 'c' || label === 'pre' || label === 'preview') out += `rc${num}`;
    else if (label === 'dev') out += `.dev${num}`;
    else out += `+${pre.replace(/[^0-9A-Za-z]+/g, '.')}`;
  }
  if (build) {
    const local = build.replace(/[^0-9A-Za-z]+/g, '.');
    out += out.includes('+') ? `.${local}` : `+${local}`;
  }
  return out;
}

export function freshDevVersion() {
  return `${DEV_MAJOR}.${Math.floor(Date.now() / 1000)}.0`;
}

export const CLI_PACKAGE_DIRS = [
  'packages/cli-darwin-arm64',
  'packages/cli-linux-x64',
  'packages/cli-linux-arm64',
  'packages/cli-win32-x64',
];

// The Python client's pyproject.toml version tracks the @pyxis-llm/* version.
export const PYPROJECT_PATH = 'packages/python-client/pyproject.toml';

export const ALL_PACKAGE_DIRS = [
  'packages/core',
  'packages/client',
  'packages/react',
  ...CLI_PACKAGE_DIRS,
];
