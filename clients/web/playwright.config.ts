import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  retries: 0,
  use: {
    baseURL: "http://localhost:8085",
    headless: true,
  },
  webServer: undefined, // we manage the stack externally via web-e2e.sh
});
