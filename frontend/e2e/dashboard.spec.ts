import { expect, test } from "@playwright/test";
import { readFileSync, writeFileSync } from "node:fs";
const fixture = JSON.parse(readFileSync(process.env.NOX_SPLIT_FIXTURE!, "utf8")) as {
  base: string;
  adminSession: string;
  memberSession: string;
  apiKey: string;
  vaultId: string;
};
test("anonymous login, assets and responsive layout work through the frontend image", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(fixture.base + "/vault-dashboard");
  await expect(page.getByRole("link", { name: "Continue with Google" })).toHaveAttribute(
    "href",
    "/auth/google/start?redirect=/vault-dashboard",
  );
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await expect(page.getByLabel("API key", { exact: true })).toHaveCount(0);
});
test("admin manages credentials, users and vaults through authenticated JSON APIs", async ({
  page,
  context,
}, testInfo) => {
  await context.addCookies([
    { name: "nox_sync_session", value: fixture.adminSession, url: fixture.base },
  ]);
  await page.goto(fixture.base + "/vault-dashboard");
  await expect(page.getByLabel("API key", { exact: true })).toHaveValue(fixture.apiKey);
  await expect(page.getByLabel("API key", { exact: true })).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "Show API key" }).click();
  await expect(page.getByLabel("API key", { exact: true })).toHaveAttribute("type", "text");
  await expect(page.getByText("r9007199254740993")).toBeVisible();
  const download = page.waitForEvent("download");
  await page.getByRole("link", { name: "Download vault", exact: true }).click();
  expect((await download).suggestedFilename()).toBe("Notes.zip");
  const browserMutations: string[] = [];
  page.on("request", (request) => {
    if (["POST", "PUT", "DELETE"].includes(request.method()))
      browserMutations.push(new URL(request.url()).pathname);
  });
  // A real UI error keeps the input and never replays the write automatically.
  await page.route(
    "**/api/admin/users",
    async (route) => {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          code: "SERVER_ERROR",
          message: "Temporary administration failure.",
        }),
      });
    },
    { times: 1 },
  );
  await page.getByLabel("Google account email").fill("ui@example.invalid");
  await page.getByRole("button", { name: "Add user", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveText("Temporary administration failure.");
  await expect(page.getByLabel("Google account email")).toHaveValue("ui@example.invalid");
  await page.getByRole("button", { name: "Add user", exact: true }).click();
  await expect(page.getByRole("row").filter({ hasText: "ui@example.invalid" })).toBeVisible();
  await page
    .getByRole("row")
    .filter({ hasText: "ui@example.invalid" })
    .getByRole("button", { name: "Disable user", exact: true })
    .click();
  await expect(
    page
      .getByRole("row")
      .filter({ hasText: "ui@example.invalid" })
      .getByRole("button", { name: "Enable user", exact: true }),
  ).toBeVisible();
  const testAccountRow = page.getByRole("row").filter({ hasText: "ui@example.invalid" });
  await testAccountRow.getByRole("button", { name: "Enable user", exact: true }).click();
  await testAccountRow.getByRole("button", { name: "Make admin", exact: true }).click();
  await expect(
    testAccountRow.getByRole("button", { name: "Make member", exact: true }),
  ).toBeVisible();
  await expect(
    testAccountRow.getByRole("button", { name: "Delete user", exact: true }),
  ).toBeEnabled();
  await testAccountRow.getByRole("button", { name: "Disable user", exact: true }).click();
  await testAccountRow.getByRole("button", { name: "Enable user", exact: true }).click();
  await testAccountRow.getByRole("button", { name: "Make member", exact: true }).click();
  await testAccountRow.getByRole("button", { name: "Make admin", exact: true }).click();
  const managerRow = page.getByRole("row").filter({ hasText: "admin@example.invalid" });
  await expect(managerRow).toContainText("Admin Manager");
  await expect(managerRow.getByRole("button", { name: "Delete user", exact: true })).toBeDisabled();
  await testAccountRow.getByRole("button", { name: "Delete user", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Delete user", exact: true })
    .getByRole("button", { name: "Delete user", exact: true })
    .click();
  await expect(page.getByRole("row").filter({ hasText: "ui@example.invalid" })).toHaveCount(0);
  await page.getByRole("button", { name: "Delete vault", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Delete vault", exact: true })
    .getByRole("button", { name: "Delete vault", exact: true })
    .click();
  await expect(page.getByText("No vaults yet.", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Restore deleted vaults", exact: true }).click();
  await page.getByRole("button", { name: "Restore vault", exact: true }).click();
  await expect(page.getByText("r9007199254740993")).toBeVisible();
  await page.getByLabel("Yard address").fill("https://yard.example.invalid");
  await page.getByRole("button", { name: "Save Yard configuration" }).click();
  await expect(page.getByText("Configuration saved · Awaiting pairing")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("Yard address")).toHaveValue("https://yard.example.invalid");
  // Another administrator changes the persisted revision while this tab is stale.
  const connections = (await (
    await page.request.get(fixture.base + "/api/admin/connections")
  ).json()) as { service: string; revision: string }[];
  const yard = connections.find((connection) => connection.service === "yard")!;
  const concurrent = await page.request.put(fixture.base + "/api/admin/connections/yard", {
    headers: { "X-Nox-Request": "1", "If-Match": `"${yard.revision}"` },
    data: { origin: "https://yard-new.example.invalid:8443" },
  });
  expect(concurrent.status()).toBe(200);
  await page.getByLabel("Yard address").fill("https://stale.example.invalid");
  await page.getByRole("button", { name: "Save Yard configuration" }).click();
  await expect(page.getByRole("alert")).toContainText("Configuration changed");
  await expect(page.getByLabel("Yard address")).toHaveValue("https://stale.example.invalid");
  await page.reload();
  await expect(page.getByLabel("Yard address")).toHaveValue(
    "https://yard-new.example.invalid:8443",
  );
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Remove Yard configuration" }).click();
  await expect(page.getByRole("button", { name: "Remove Yard configuration" })).toHaveCount(0);
  await page.getByLabel("Gateway address").fill("https://192.168.10.10:8443");
  await page.getByRole("button", { name: "Save Gateway configuration" }).click();
  await expect(page.getByLabel("Gateway address")).toHaveValue("https://192.168.10.10:8443");
  await expect(page.getByText("Configuration saved · Awaiting pairing")).toBeVisible();
  expect(browserMutations.some((path) => path.startsWith("/vault-dashboard/"))).toBe(false);
  expect(browserMutations).toContain("/api/admin/users");
  expect(browserMutations).toContain("/api/admin/connections/yard");
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "Generate new API key", exact: true }).click();
  await expect(page.getByLabel("API key", { exact: true })).not.toHaveValue(fixture.apiKey);
  writeFileSync(
    process.env.NOX_SPLIT_FIXTURE!,
    JSON.stringify({
      ...fixture,
      apiKey: await page.getByLabel("API key", { exact: true }).inputValue(),
    }),
  );
  await page.getByRole("button", { name: "Manage Suite integrations" }).click();
  await expect(
    page.getByRole("button", { name: "Prepare signing key", exact: true }),
  ).toBeEnabled();
  expect(await page.evaluate(async () => (await fetch("/api/nox/keys")).status)).toBe(403);
  await expect(
    page.getByRole("button", { name: "Generate new API key", exact: true }),
  ).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath("dashboard.png"), fullPage: true });
  await page.getByRole("button", { name: "Log out", exact: true }).click();
  await expect(page.getByRole("link", { name: "Continue with Google" })).toBeVisible();
});
test("a member retains their frontend and cannot see admin controls", async ({ page, context }) => {
  await context.addCookies([
    { name: "nox_sync_session", value: fixture.memberSession, url: fixture.base },
  ]);
  await page.goto(fixture.base + "/vault-dashboard");
  await expect(page.getByLabel("API key", { exact: true })).toBeVisible();
  await expect(page.getByText("User management")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Add user", exact: true })).toHaveCount(0);
  await expect(page.getByText("Service connections", { exact: true })).toHaveCount(0);
  expect((await page.request.get(fixture.base + "/api/admin/connections")).status()).toBe(403);
  expect((await page.request.get(fixture.base + "/api/admin/users")).status()).toBe(403);
});
