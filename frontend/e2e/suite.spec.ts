import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";

test.use({
  launchOptions: {
    args: ["--host-resolver-rules=MAP nox-suite.test 127.0.0.1", "--no-proxy-server"],
  },
});
test.skip(!process.env.NOX_SUITE_FIXTURE, "Requires the disposable local protocol fixture.");
test("HTTP dashboard connects the local participant automatically and recovers a lost response", async ({
  page,
  context,
}) => {
  test.setTimeout(90000);
  const fixture = JSON.parse(readFileSync(process.env.NOX_SUITE_FIXTURE!, "utf8")) as {
    base: string;
    adminSession: string;
    statePath: string;
  };
  await context.addCookies([
    { name: "nox_sync_session", value: fixture.adminSession, url: fixture.base },
  ]);
  page.on("dialog", (dialog) => dialog.accept());
  await page.goto(fixture.base + "/vault-dashboard");
  expect(await page.evaluate(() => window.isSecureContext)).toBe(false);
  await page.getByLabel("Yard address", { exact: true }).fill("http://yard.native.invalid:8095");
  await page.getByRole("button", { name: "Save Yard configuration", exact: true }).click();
  await page.getByRole("button", { name: "Verify Yard", exact: true }).click();
  await expect(page.getByText("Local protocol participant fixture · verified")).toBeVisible();
  await expect(page.getByRole("button", { name: "Connect Yard", exact: true })).toBeEnabled();
  const responses: string[] = [];
  page.on("response", async (response) => {
    if (new URL(response.url()).pathname.startsWith("/api/admin/connections")) {
      try {
        responses.push(await response.text());
      } catch {
        /* Intentional response loss. */
      }
    }
  });
  await page.route(
    "**/api/admin/connections/yard/connect",
    async (route) => {
      const destination = new URL(route.request().url());
      const authority = destination.host;
      destination.hostname = "127.0.0.1";
      await route.fetch({
        url: destination.href,
        headers: { ...route.request().headers(), host: authority },
      });
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page.getByRole("button", { name: "Connect Yard", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("Request could not be confirmed");
  await page.reload();
  await expect(
    page.getByText("Service registered · Availability tracked separately"),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Connect Yard", exact: true })).toHaveCount(0);
  expect(JSON.parse(readFileSync(fixture.statePath, "utf8")).deliveries).toBe(1);
  for (const value of responses)
    expect(value).not.toMatch(/noxsvc_|"secret"|"nonce"|"serviceCredential"/);
  expect(await page.locator("textarea").count()).toBe(0);
  // Human management stays usable in this actual insecure browser context.
  await page.getByRole("button", { name: "Manage Suite integrations" }).click();
  await expect(page.getByRole("button", { name: "Create pairing", exact: true })).toBeEnabled();
  await expect(page.getByText("nox-yard", { exact: true }).first()).toBeVisible();
  await page.getByRole("button", { name: "Prepare signing key", exact: true }).click();
  await expect(
    page.getByText("Signing key prepared. It is published before activation."),
  ).toBeVisible();
  const keys = await page.evaluate(
    async () => (await fetch("/api/admin/suite/operations/keys")).status,
  );
  expect(keys).toBe(200);
  const network = await page.evaluate(async () => (await fetch("/api/nox/keys")).status);
  expect(network).toBe(403);
  const native = await page.evaluate(async () => (await fetch("/api/account")).status);
  expect(native).toBe(200);
});
