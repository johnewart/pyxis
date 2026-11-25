import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vitest/config';

const here = path.dirname(fileURLToPath(import.meta.url));
const clientDist = (file: string): string => path.resolve(here, '..', '..', 'client', 'dist', file);
const coreDist = (file: string): string => path.resolve(here, '..', '..', 'core', 'dist', file);

export default defineConfig({
  esbuild: {
    jsx: 'automatic',
  },
  // @pyxis-llm/react is consumed via `file:..` (npm symlinks it), so its own
  // node_modules copies of these peer deps are real, separate installs from
  // this package's copies. Without deduping, React/Mantine end up with two
  // live module instances (two React contexts, two hook dispatchers), which
  // breaks hooks with "Cannot read properties of null" or "no provider
  // found" errors even though everything is "correctly" installed. Forcing
  // resolution to this package's copy is the standard fix for linked
  // monorepo packages.
  resolve: {
    // The shared client e2e harness (../../client/e2e/harness.ts) imports
    // @pyxis-llm/core, but packages/client/e2e has no node_modules here, so point
    // those imports at the built core package directly.
    alias: [
      { find: /^@pyxis-llm\/core\/node$/, replacement: coreDist('node.js') },
      { find: /^@pyxis-llm\/core$/, replacement: coreDist('index.js') },
      { find: /^@pyxis-llm\/client\/gen\/(.*)$/, replacement: clientDist('gen/$1') },
      { find: /^@pyxis-llm\/client\/(node|web)$/, replacement: clientDist('$1.js') },
      { find: /^@pyxis-llm\/client$/, replacement: clientDist('index.js') },
    ],
    dedupe: ['react', 'react-dom', '@mantine/core', '@mantine/hooks'],
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./setup.ts'],
    // One real llmd for the whole run (see globalSetup.ts); files share it.
    globalSetup: ['./globalSetup.ts'],
    // Tests drive a real llmd process over real gRPC, then
    // renders the streamed output through real React components (including a
    // real mermaid render attempt) — give it real wall-clock room instead of
    // unit-test-speed defaults.
    testTimeout: 30_000,
    hookTimeout: 30_000,
    fileParallelism: false,
  },
});
