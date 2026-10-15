import { defineConfig } from "@playwright/test";

// Real WebRTC in Chromium, the engine WebView2 is built on (ADR 0025).
export default defineConfig({
  testDir: "e2e",
  testMatch: "*.e2e.ts",
  use: { baseURL: "http://127.0.0.1:5179", browserName: "chromium" },
  webServer: {
    command: "npx vite --port 5179 --strictPort --host 127.0.0.1",
    url: "http://127.0.0.1:5179/e2e/harness.html",
    reuseExistingServer: !process.env.CI,
  },
});
