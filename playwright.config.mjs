import { defineConfig, devices } from "@playwright/test";
import { chromiumLaunchOptions } from "./tests/e2e/chromium-launch.mjs";

export default defineConfig({
  testDir: "tests/e2e",
  timeout: 30_000,
  use: { baseURL: "http://127.0.0.1:8765" },
  webServer: {
    command: "node tests/e2e/static-server.mjs",
    url: "http://127.0.0.1:8765",
    reuseExistingServer: true,
  },
  projects: [
    {
      name: "chromium",
      use: {
        browserName: "chromium",
        launchOptions: chromiumLaunchOptions(),
      },
    },
    { name: "firefox", use: { browserName: "firefox" } },
    { name: "webkit", use: { browserName: "webkit" } },
    { name: "mobile-chromium", use: { ...devices["Pixel 5"], browserName: "chromium" } },
    { name: "mobile-webkit", use: { ...devices["iPhone 13"], browserName: "webkit" } },
  ],
});
