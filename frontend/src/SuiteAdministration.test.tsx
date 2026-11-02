import { afterEach, beforeEach, expect, test, vi } from "vite-plus/test";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { webcrypto } from "node:crypto";
import { SuiteAdministration } from "./SuiteAdministration";
import type { SuiteAdministration as Administration } from "./suite-model";
import {
  journalKey,
  recoveryKey,
  preparePending,
  readPending,
  requestFingerprint,
  sendSuiteRequest,
  SuiteRequestFailure,
} from "./suite-requests";

const state: Administration = {
  configuration: {
    backendOrigin: "http://nox-backend",
    canManage: true,
    canEnroll: true,
    reason: "",
    directMcpEnabled: true,
    suiteMcpEnabled: true,
  },
  state: {
    issuer: "urn:nox:test",
    keyRevision: "keys-one",
    registryRevision: "registry-one",
    observedAt: "2026-10-31T10:00:00.000Z",
    signingKeys: [],
    services: [],
    pairings: [],
    products: [],
    observations: [],
    blockedProducts: [],
  },
};
const response = (value: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => value,
});
beforeEach(() => {
  sessionStorage.clear();
  vi.stubGlobal("crypto", webcrypto);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});
async function open() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Manage Suite integrations" }));
  await screen.findByRole("button", { name: "Prepare signing key" });
  return user;
}
async function fillProduct(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Paired product ID", { exact: true }), "future-product");
  await user.type(
    screen.getByLabelText("Paired product local Suite origin"),
    "http://future-product",
  );
}

test("a missing local channel disables Suite changes without disabling state inspection", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      response({
        ...state,
        configuration: {
          ...state.configuration,
          canManage: false,
          reason: "The local Suite channel is unavailable.",
        },
      }),
    ),
  );
  render(<SuiteAdministration actorId="admin" />);
  await open();
  expect(
    screen.getByRole("button", { name: "Verify and register product" }).matches(":disabled"),
  ).toBe(true);
  expect(screen.getByText("The local Suite channel is unavailable.")).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Refresh Suite state" }) as HTMLButtonElement).disabled,
  ).toBe(false);
});

test("a lost response survives reload and explicit retry uses the original key and body", async () => {
  const writes: RequestInit[] = [];
  let lose = true;
  const fetcher = vi.fn((_path: string, options?: RequestInit) => {
    if (options?.method) {
      writes.push(options);
      if (lose) {
        lose = false;
        return Promise.reject(Error("connection lost"));
      }
      return Promise.resolve(response({ contractVersion: "1.0" }, 201));
    }
    return Promise.resolve(response(state));
  });
  vi.stubGlobal("fetch", fetcher);
  const view = render(<SuiteAdministration actorId="admin" />);
  let user = await open();
  await fillProduct(user);
  await user.click(screen.getByRole("button", { name: "Verify and register product" }));
  await screen.findByText("Operation awaiting confirmation");
  expect(writes).toHaveLength(1);
  const saved = sessionStorage.getItem(journalKey("admin"));
  expect(saved).toContain("future-product");
  view.unmount();
  render(<SuiteAdministration actorId="admin" />);
  user = await open();
  await user.click(screen.getByRole("button", { name: "Retry same Suite request" }));
  await screen.findByText("Product verified and registered. Availability is reported separately.");
  expect(writes).toHaveLength(2);
  expect(writes[1].headers).toEqual(writes[0].headers);
  expect(writes[1].body).toEqual(writes[0].body);
  expect(sessionStorage.getItem(journalKey("admin"))).toBeNull();
  expect(screen.queryByLabelText("Setup information")).toBeNull();
});

test("manual secret operations cannot be journaled or sent", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const request = {
    path: "/api/nox/pairings/p/complete",
    method: "POST" as const,
    body: '{"proof":"private-hmac"}',
    credential: "noxsvc_private",
    message: "Done",
  };
  await expect(preparePending("admin", request)).rejects.toMatchObject({ uncertain: false });
  await expect(sendSuiteRequest(request, "old-key")).rejects.toMatchObject({ uncertain: false });
  expect(sessionStorage.getItem(journalKey("admin"))).toBeNull();
  expect(readPending("another-user")).toBeNull();
  expect(fetcher).not.toHaveBeenCalled();
});

test("a stale revision displays the actual Suite diagnostic and preserves form inputs", async () => {
  const fetcher = vi.fn((_path: string, options?: RequestInit) =>
    Promise.resolve(
      options?.method
        ? response(
            {
              error: {
                code: "REGISTRATION_PRECONDITION_FAILED",
                message: "Trust configuration changed. Reconcile before retrying.",
              },
            },
            412,
          )
        : response(state),
    ),
  );
  vi.stubGlobal("fetch", fetcher);
  render(<SuiteAdministration actorId="admin" />);
  const user = await open();
  await fillProduct(user);
  await user.click(screen.getByRole("button", { name: "Verify and register product" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("Trust configuration changed");
  expect(
    (screen.getByLabelText("Paired product ID", { exact: true }) as HTMLInputElement).value,
  ).toBe("future-product");
  expect(sessionStorage.getItem(journalKey("admin"))).toBeNull();
  expect(fetcher.mock.calls.filter(([, options]) => options?.method)).toHaveLength(1);
});

test("stale readiness becomes unknown without inventing membership or backend probes", async () => {
  const product = {
    productId: "future-product",
    associationId: "a",
    revision: "r",
    declaration: {
      displayName: "Future product",
      productVersion: "1",
      capabilities: { suiteMcp: { path: "/suite/mcp" } },
    },
    connection: { baseUrl: "http://future-product" },
  };
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      response({
        ...state,
        state: {
          ...state.state,
          products: [product],
          observations: [
            {
              observation: { productId: "future-product", source: "gateway", state: "ready" },
              freshUntil: "2000-01-01T00:00:00.000Z",
            },
          ],
        },
      }),
    ),
  );
  render(<SuiteAdministration actorId="admin" />);
  await open();
  expect(screen.getByText("Lifecycle: unknown · MCP readiness: unknown")).toBeTruthy();
  expect(screen.getByText("Registered · trust unavailable; reconcile association")).toBeTruthy();
});

test("Suite state failure leaves refresh available and does not expose mutation controls", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(response({ error: { message: "Integration is unavailable." } }, 503)),
  );
  render(<SuiteAdministration actorId="admin" />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Manage Suite integrations" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("Integration is unavailable");
  expect(screen.queryByRole("button", { name: "Verify and register product" })).toBeNull();
  expect(
    (screen.getByRole("button", { name: "Refresh Suite state" }) as HTMLButtonElement).disabled,
  ).toBe(false);
});

test("confirmed 204 removal is handled and non-JSON success stays uncertain", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true, status: 204 }));
  await expect(
    sendSuiteRequest(
      { path: "/api/nox/products/p", method: "DELETE", message: "Removed" },
      "operation",
    ),
  ).resolves.toBeNull();
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => {
        throw Error("not JSON");
      },
    }),
  );
  await expect(
    sendSuiteRequest({ path: "/api/nox/keys", method: "POST", message: "Prepared" }, "operation"),
  ).rejects.toMatchObject({ uncertain: true });
  expect(new SuiteRequestFailure("lost", true).uncertain).toBe(true);
});

test("an old private operation requires reconciliation without credential inputs", async () => {
  sessionStorage.setItem(
    journalKey("admin"),
    JSON.stringify({
      key: "old-key",
      fingerprint: "old-fingerprint",
      needsCredential: true,
      request: { path: "/api/nox/pairings/p/ack", method: "POST", message: "Done" },
    }),
  );
  const fetcher = vi.fn((_path: string, _options?: RequestInit) =>
    Promise.resolve(response(state)),
  );
  vi.stubGlobal("fetch", fetcher);
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(<SuiteAdministration actorId="admin" />);
  const user = await open();
  await screen.findByRole("alert");
  expect(screen.queryByLabelText("Original service credential")).toBeNull();
  expect(screen.queryByRole("button", { name: "Retry same Suite request" })).toBeNull();
  await user.click(
    screen.getByRole("button", { name: "Reconcile and clear unreadable operation" }),
  );
  await screen.findByText("State reconciled. The saved operation record was closed.");
  expect(sessionStorage.getItem(journalKey("admin"))).toBeNull();
  expect(fetcher.mock.calls.every(([, options]) => !options?.method)).toBe(true);
});

test("old manual setup recovery is never replayed", async () => {
  sessionStorage.setItem(recoveryKey("admin"), JSON.stringify({ key: "old-pairing-key" }));
  const fetcher = vi.fn((_path: string, _options?: RequestInit) =>
    Promise.resolve(response(state)),
  );
  vi.stubGlobal("fetch", fetcher);
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(<SuiteAdministration actorId="admin" />);
  const user = await open();
  await screen.findByText(/A previous manual setup record exists/);
  expect(screen.queryByRole("button", { name: "Recover last setup response" })).toBeNull();
  await user.click(
    screen.getByRole("button", { name: "Reconcile and clear unreadable operation" }),
  );
  await screen.findByText("State reconciled. The saved operation record was closed.");
  expect(sessionStorage.getItem(recoveryKey("admin"))).toBeNull();
  expect(fetcher.mock.calls.every(([, options]) => !options?.method)).toBe(true);
});

test("local administration needs no obsolete network fields and exposes no manual setup", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      response({
        ...state,
        state: {
          ...state.state,
          services: [
            {
              serviceId: "local-service",
              productId: "test-product",
              associationId: "association",
              baseUrl: "http://test-product",
              purpose: "product",
              state: "active",
              revision: "service-rev",
              headRevision: "head-rev",
              current: true,
              deployments: [],
              credentials: [
                {
                  credentialId: "credential",
                  revision: "credential-rev",
                  state: "active",
                  current: true,
                },
              ],
            },
          ],
          pairings: [
            {
              pairingId: "pending",
              productId: "test-product",
              baseUrl: "http://test-product",
              purpose: "product",
              state: "provisioned",
              revision: "pairing-rev",
              expiresAt: "2099-01-01T00:00:00.000Z",
            },
          ],
        },
      }),
    ),
  );
  render(<SuiteAdministration actorId="admin" />);
  await open();
  expect(screen.getByRole("button", { name: "Prepare signing key" }).matches(":disabled")).toBe(
    false,
  );
  expect(screen.queryByRole("button", { name: "Create pairing" })).toBeNull();
  expect(screen.queryByLabelText("Product pairing proof JSON")).toBeNull();
  expect(screen.queryByRole("button", { name: "Rotate credential for test-product" })).toBeNull();
  expect(screen.getByRole("button", { name: "Revoke service test-product" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Cancel pairing for test-product" })).toBeTruthy();

  expect(screen.queryByLabelText("Setup information")).toBeNull();
});

test("a malformed successful snapshot fails only the Suite pane", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ ok: true })));
  render(<SuiteAdministration actorId="admin" />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Manage Suite integrations" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("Invalid Suite administration response");
  expect(screen.queryByRole("button", { name: "Verify and register product" })).toBeNull();
});

test("operation recovery works without secure-context WebCrypto APIs", async () => {
  vi.stubGlobal("crypto", { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) });
  const request = { path: "/api/nox/keys", method: "POST" as const, message: "Prepared" };
  const pending = await preparePending("admin", request);
  expect(pending.key).toMatch(
    /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/,
  );
  expect(pending.fingerprint).toHaveLength(64);
  expect(await requestFingerprint(request)).toBe(pending.fingerprint);
  const fetcher = vi.fn().mockResolvedValue(response({ ok: true }));
  vi.stubGlobal("fetch", fetcher);
  await sendSuiteRequest(request, pending.key);
  expect(fetcher).toHaveBeenCalledWith(
    "/api/admin/suite/operations/keys",
    expect.objectContaining({
      headers: expect.objectContaining({ "Idempotency-Key": pending.key, "X-Nox-Request": "1" }),
    }),
  );
});

test("browser administration never forwards a participant service credential", async () => {
  const fetcher = vi.fn();
  vi.stubGlobal("fetch", fetcher);
  const oldRequest = {
    path: "/api/nox/pairings/p/ack",
    method: "POST" as const,
    credential: "noxsvc_secret",
    message: "Done",
  };
  await expect(sendSuiteRequest(oldRequest, "key")).rejects.toMatchObject({ uncertain: false });
  expect(fetcher).not.toHaveBeenCalled();
});
