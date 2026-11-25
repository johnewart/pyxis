import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    environment: 'node',
    // Each test spawns a real llmd process and drives it over real gRPC;
    // give it real wall-clock room instead of unit-test-speed defaults.
    testTimeout: 20_000,
    hookTimeout: 20_000,
    fileParallelism: false,
  },
});
