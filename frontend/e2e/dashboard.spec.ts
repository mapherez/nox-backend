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
test("admin manages credentials, users and vaults using the preserved native forms", async ({
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
  await page.getByLabel("Google account email").fill("ui@example.invalid");
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
  await page
    .getByRole("row")
    .filter({ hasText: "ui@example.invalid" })
    .getByRole("button", { name: "Delete user", exact: true })
    .click();
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
});
