import { defineConfig } from 'vitest/config';

// Pure unit tests for this package's hand-written (non-generated) source —
// no llmd binary, no network. Separate from e2e/ (which spawns a real llmd
// process; see e2e/vitest.config.ts) so `npm test` here stays fast and has
// no external dependencies.
export default defineConfig({
  test: {
    environment: 'node',
    include: ['test/**/*.test.ts'],
  },
});
