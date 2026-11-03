import { defineConfig } from "@playwright/test";

// D-pad focus tests for the TV interface. See e2e/README.md.
//
// By default this starts the Vite dev server and serves a fake library from
// e2e/mock.ts, so runs are offline and identical every time. Set E2E_BASE_URL
// to point at a running UI instead (e.g. `npm run dev:tv`, which proxies the
// real server); add E2E_USER / E2E_PASS if it asks you to sign in.
const live = process.env.E2E_BASE_URL;
const port = 5180;

export default defineConfig({
  testDir: "e2e",
  timeout: 90_000,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: live || `http://localhost:${port}`,
    // The Fire TV / Shield WebView lays the 1080p screen out as 960x540 CSS px at DPR 2.
    viewport: { width: 960, height: 540 },
    deviceScaleFactor: 2,
    hasTouch: false,
    colorScheme: "dark",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "tv-960x540@2x" }],
  webServer: live
    ? undefined
    : {
        command: `npx vite --port ${port} --strictPort`,
        url: `http://localhost:${port}`,
        reuseExistingServer: !process.env.CI,
        timeout: 60_000,
      },
});
