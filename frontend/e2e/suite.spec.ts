import { expect, test } from "@playwright/test";
import { createHash, createHmac } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

test.use({ ignoreHTTPSErrors: true }); // Explicit disposable CA fixture; production pin/PKI checks stay enabled.
test.skip(!process.env.NOX_SUITE_FIXTURE, "Requires the disposable HTTPS protocol fixture.");
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value !== null && typeof value === "object")
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${canonical((value as Record<string, unknown>)[key])}`)
      .join(",")}}`;
  return JSON.stringify(value);
}
test("native Suite setup recovers a lost response, pairs, registers and rotates through actual HTTPS", async ({
  page,
  context,
}) => {
  test.setTimeout(90000);
  const fixture = JSON.parse(readFileSync(process.env.NOX_SUITE_FIXTURE!, "utf8")) as {
    base: string;
    adminSession: string;
    apiKey: string;
    productOrigin: string;
    declaration: Record<string, unknown>;
    pin: string;
  };
  await context.addCookies([
    { name: "nox_sync_session", value: fixture.adminSession, url: fixture.base },
  ]);
  page.on("dialog", (dialog) => dialog.accept());
  await page.goto(fixture.base + "/vault-dashboard");
  await page.getByRole("button", { name: "Manage Suite integrations" }).click();
  await expect(page.getByRole("button", { name: "Create pairing", exact: true })).toBeEnabled();
  const keys: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/api/nox/pairings")
      keys.push(request.headers()["idempotency-key"]);
  });
  await page.route(
    "**/api/nox/pairings",
    async (route) => {
      await route.fetch();
      await route.abort("failed");
    },
    { times: 1 },
  );
  await page.getByLabel("Product ID", { exact: true }).fill("suite-browser-fixture");
  await page.getByLabel("Product HTTPS address", { exact: true }).fill(fixture.productOrigin);
  await page.getByRole("button", { name: "Create pairing", exact: true }).click();
  await expect(page.getByText("Operation awaiting confirmation")).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "Manage Suite integrations" }).click();
  await page.getByRole("button", { name: "Retry same Suite request" }).click();
  const info = page.getByLabel("Setup information", { exact: true });
  await expect(info).toBeVisible();
  const intent = JSON.parse(await info.inputValue()) as {
    pairingId: string;
    nonce: string;
    secret: string;
    productId: string;
    baseUrl: string;
  };
  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  expect(
    await page.evaluate(() =>
      JSON.stringify(
        Object.fromEntries(
          Object.keys(sessionStorage).map((key) => [key, sessionStorage.getItem(key)]),
        ),
      ),
    ),
  ).not.toContain(intent.secret);
  await page.getByRole("button", { name: "Clear setup information" }).click();
  const binding = {
    contractVersion: "1.0",
    pairingId: intent.pairingId,
    nonce: intent.nonce,
    productId: intent.productId,
    baseUrl: intent.baseUrl,
    declarationFingerprint: createHash("sha256")
      .update(canonical(fixture.declaration))
      .digest("base64url"),
    tlsPublicKeyFingerprint: fixture.pin,
  };
  const proof = {
    contractVersion: "1.0",
    binding,
    proof: createHmac("sha256", Buffer.from(intent.secret, "base64url"))
      .update(canonical(binding))
      .digest("base64url"),
  };
  const pairing = page
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: "suite-browser-fixture · pending" }) });
  await pairing.locator("summary").click();
  await pairing.getByLabel("Product pairing proof JSON").fill(JSON.stringify(proof));
  await pairing
    .getByRole("button", { name: "Complete pairing for suite-browser-fixture", exact: true })
    .click();
  await expect(info).toBeVisible();
  const provision = JSON.parse(await info.inputValue()) as {
    serviceCredential: string;
    service: { serviceId: string; credentialId: string };
  };
  // The explicit protocol participant stores provisioning before its ACK.
  writeFileSync(
    join(dirname(process.env.NOX_SUITE_FIXTURE!), "participant-provisioning.json"),
    JSON.stringify(provision),
    { mode: 0o600 },
  );
  await page.getByRole("button", { name: "Clear setup information" }).click();
  const provisioned = page
    .locator("details")
    .filter({ has: page.locator("summary", { hasText: "suite-browser-fixture · provisioned" }) });
  if (!(await provisioned.evaluate((element) => (element as HTMLDetailsElement).open)))
    await provisioned.locator("summary").click();
  await provisioned
    .getByLabel("Saved product service credential")
    .fill(provision.serviceCredential);
  await provisioned
    .getByRole("button", {
      name: "Acknowledge saved provisioning for suite-browser-fixture",
      exact: true,
    })
    .click();
  await expect(
    page.getByText("Service acknowledged. Verify and register its product separately."),
  ).toBeVisible();
  await page.getByLabel("Paired product ID", { exact: true }).fill("suite-browser-fixture");
  await page
    .getByLabel("Paired product HTTPS address", { exact: true })
    .fill(fixture.productOrigin);
  await page.getByRole("button", { name: "Verify and register product", exact: true }).click();
  await expect(page.getByText("Registered · Suite MCP declared", { exact: true })).toBeVisible();
  await expect(page.getByText("Lifecycle: unknown · MCP readiness: unknown")).toBeVisible();
  const service = page.locator("details").filter({
    has: page.locator("summary", { hasText: "suite-browser-fixture · product · active" }),
  });
  await service.locator("summary").click();
  await service
    .getByRole("button", { name: "Rotate credential for suite-browser-fixture", exact: true })
    .click();
  await expect(info).toBeVisible();
  const replacement = JSON.parse(await info.inputValue()) as typeof provision;
  writeFileSync(
    join(dirname(process.env.NOX_SUITE_FIXTURE!), "participant-replacement.json"),
    JSON.stringify(replacement),
    { mode: 0o600 },
  );
  expect(
    await page.evaluate(() =>
      JSON.stringify(
        Object.fromEntries(
          Object.keys(sessionStorage).map((key) => [key, sessionStorage.getItem(key)]),
        ),
      ),
    ),
  ).not.toContain(replacement.serviceCredential);
  await page.getByRole("button", { name: "Clear setup information" }).click();
  if (!(await service.evaluate((element) => (element as HTMLDetailsElement).open)))
    await service.locator("summary").click();
  await service.getByLabel("Saved replacement credential").fill(replacement.serviceCredential);
  await service
    .getByRole("button", { name: "Acknowledge replacement for suite-browser-fixture", exact: true })
    .click();
  await expect(page.getByText("Replacement acknowledged; predecessor revoked.")).toBeVisible();
  expect(
    (
      await page.request.get(fixture.base + "/api/nox/services/" + provision.service.serviceId, {
        headers: { Authorization: "Bearer " + provision.serviceCredential },
      })
    ).status(),
  ).toBe(401);
  await page.getByRole("button", { name: "Prepare signing key", exact: true }).click();
  const activate = page.getByRole("button", { name: /^Activate signing key / });
  await expect(activate).toBeVisible();
  await activate.click();
  await expect(page.getByText("Prepared signing key activated.")).toBeVisible();
  await page
    .getByRole("button", { name: "Remove registration suite-browser-fixture", exact: true })
    .click();
  await expect(page.getByText("Registration removed and its association retired.")).toBeVisible();
  expect(
    (await page.request.get(fixture.base + "/api/nox/products/suite-browser-fixture")).status(),
  ).toBe(404);
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  // A failed Suite refresh blocks Suite changes without breaking native controls.
  await page.route(
    "**/api/admin/suite",
    (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: { message: "Temporary fixture failure." } }),
      }),
    { times: 1 },
  );
  await page.getByRole("button", { name: "Refresh Suite state" }).click();
  await expect(page.getByRole("alert")).toContainText("Temporary fixture failure.");
  await expect(page.getByRole("button", { name: "Create pairing", exact: true })).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Generate new API key", exact: true }),
  ).toBeEnabled();
});
