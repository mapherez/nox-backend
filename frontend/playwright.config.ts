import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./e2e",
  workers: 1,
  fullyParallel: false,
  use: { browserName: "chromium", headless: true, trace: "retain-on-failure" },
  reporter: "list",
});
