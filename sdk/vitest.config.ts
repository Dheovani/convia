import { defineConfig } from 'vitest/config'

/*
The unit tests are the ones under `src`, and only those.

`e2e/` holds Playwright specs, which look like tests to anything scanning for
them and are not: they need a browser and a running Convia, and vitest loading
one fails in a way that says nothing about either.
*/
export default defineConfig({
  test: {
    include: ['src/**/*.test.ts'],
  },
})
