import { defineConfig, devices } from '@playwright/test'

/*
The journeys a call cannot be trusted without, run end to end: a Convia that is
already running, a real media server that reports to it, and a real browser with
made-up devices. They are few on purpose — the component tests cover the states,
and these cover what only the real pieces together can show.

They run one at a time. Each creates its own people and rooms, but the media
server and the budgets Convia keeps per address are shared, and a journey that
competes for them proves less than one that does not.

CONVIA_E2E_URL is the Convia to drive, and CONVIA_E2E_CHANNEL picks an installed
browser instead of the bundled Chromium (`msedge`, say). See docs/interface.md.
*/
const channel = process.env['CONVIA_E2E_CHANNEL']

export default defineConfig({
  testDir: './e2e',
  timeout: 120_000,
  expect: { timeout: 30_000 },
  workers: 1,
  forbidOnly: Boolean(process.env['CI']),
  reporter: process.env['CI'] ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    ...devices['Desktop Chrome'],
    baseURL: process.env['CONVIA_E2E_URL'] ?? 'http://localhost:8080',
    ...(channel === undefined ? {} : { channel }),
    permissions: ['microphone', 'camera'],
    launchOptions: {
      args: [
        '--use-fake-ui-for-media-stream',
        '--use-fake-device-for-media-stream',
        '--autoplay-policy=no-user-gesture-required',
      ],
    },
    trace: 'retain-on-failure',
  },
})
