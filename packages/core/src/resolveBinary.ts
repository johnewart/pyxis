/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * Locates the platform-appropriate `llmd` binary published as one of the
 * `@pyxis-llm/cli-<os>-<arch>` optionalDependencies packages, for handing to
 * {@link startLlmd}'s `binaryPath` option.
 *
 * Resolution order:
 *  1. `LLMD_BINARY_PATH` env var, if set — always wins. This is the escape
 *     hatch for running a binary built for a *different* host than the one
 *     Node is executing on (e.g. a macOS dev machine driving a Linux llmd
 *     inside a container or over a remote transport): install the matching
 *     `@pyxis-llm/cli-<os>-<arch>` package explicitly (it's just a normal npm
 *     package — nothing stops installing an "off-platform" one on purpose)
 *     and point `LLMD_BINARY_PATH` at its `bin/llmd`.
 *  2. The `@pyxis-llm/cli-<os>-<arch>` package matching `process.platform` /
 *     `process.arch`, resolved via `require.resolve`. This is the common
 *     case: npm's `os`/`cpu` package-manifest fields mean only the matching
 *     optionalDependency is ever installed, so this just works with zero
 *     configuration.
 */

import { existsSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);

/** Maps `${process.platform}-${process.arch}` to its published binary package. */
export const LLMD_PLATFORM_PACKAGES: Readonly<Record<string, string>> = Object.freeze({
  'darwin-arm64': '@pyxis-llm/cli-darwin-arm64',
  'linux-x64': '@pyxis-llm/cli-linux-x64',
  'linux-arm64': '@pyxis-llm/cli-linux-arm64',
  'win32-x64': '@pyxis-llm/cli-win32-x64',
});

export class LlmdBinaryResolutionError extends Error {}

function binaryFileName(platform: string): string {
  return platform === 'win32' ? 'llmd.exe' : 'llmd';
}

/**
 * Returns an absolute path to an `llmd` binary. Throws
 * {@link LlmdBinaryResolutionError} with remediation guidance if none can be
 * found.
 */
export function resolveLlmdBinary(
  options: { platform?: string; arch?: string } = {}
): string {
  const override = process.env.LLMD_BINARY_PATH;
  if (override) {
    if (!existsSync(override)) {
      throw new LlmdBinaryResolutionError(
        `LLMD_BINARY_PATH is set to "${override}", but no file exists there.`
      );
    }
    return override;
  }

  const platform = options.platform ?? process.platform;
  const arch = options.arch ?? process.arch;
  const key = `${platform}-${arch}`;
  const pkg = LLMD_PLATFORM_PACKAGES[key];

  if (!pkg) {
    throw new LlmdBinaryResolutionError(
      `No llmd binary is published for platform "${key}". Supported: ` +
        `${Object.keys(LLMD_PLATFORM_PACKAGES).join(', ')}. If you need to run a binary ` +
        `built for a different host (e.g. driving a remote/containerized llmd), install the ` +
        `matching @pyxis-llm/cli-<os>-<arch> package explicitly and set LLMD_BINARY_PATH to its ` +
        `bin/${binaryFileName(platform)}.`
    );
  }

  const binName = binaryFileName(platform);
  try {
    return require.resolve(`${pkg}/bin/${binName}`);
  } catch (err) {
    throw new LlmdBinaryResolutionError(
      `Could not resolve ${pkg} (expected for platform "${key}"). Install it with ` +
        `\`npm install ${pkg}\`, or set LLMD_BINARY_PATH to point at an llmd binary directly.\n` +
        `${(err as Error).message}`
    );
  }
}
