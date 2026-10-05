import { defineConfig, devices } from '@playwright/test'

/*
The SDK against a real Convia, in a real browser.

**Everything else about this package is tested with the network faked**, which
is right for deciding what it sends and what it makes of an answer, and useless
for the things that only exist outside the fake: that the session cookie
actually authenticates, that the WebSocket actually upgrades and carries events,
and that resuming from a cursor actually gets what was missed.

They run one at a time. Each creates its own people and rooms, but the budgets
Convia keeps per address are shared, and a test competing for them proves less
than one that does not.

CONVIA_E2E_URL is the Convia to drive, and the browser stays on it: the session
cookie is host-only, so every request the SDK makes has to come from a page on
Convia's own origin.
*/
export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  workers: 1,
  forbidOnly: Boolean(process.env['CI']),
  reporter: process.env['CI'] ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    ...devices['Desktop Chrome'],
    locale: 'en-US',
    baseURL: process.env['CONVIA_E2E_URL'] ?? 'http://localhost:8080',
    trace: 'retain-on-failure',
  },
})
