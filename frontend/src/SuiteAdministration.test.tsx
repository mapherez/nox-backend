import { afterEach, beforeEach, expect, test, vi } from "vite-plus/test";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
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
    backendOrigin: "https://backend.example.com",
    canManage: true,
    canEnroll: true,
    reason: "",
    directMcpEnabled: true,
    suiteMcpEnabled: true,
    allowedCidrs: ["127.0.0.0/8"],
    allowedPorts: ["443"],
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
  await screen.findByRole("button", { name: "Create pairing" });
  return user;
}
async function fillPairing(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText("Product ID", { exact: true }), "future-product");
  await user.type(
    screen.getByLabelText("Product local Suite origin"),
    "https://future.example.com",
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
  expect(screen.getByRole("button", { name: "Create pairing" }).matches(":disabled")).toBe(true);
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
      return Promise.resolve(
        response(
          {
            pairingId: "pairing-one",
            secret: "secret-result-only-in-memory",
            expiresAt: "2026-10-31T10:10:00.000Z",
          },
          201,
        ),
      );
    }
    return Promise.resolve(response(state));
  });
  vi.stubGlobal("fetch", fetcher);
  const view = render(<SuiteAdministration actorId="admin" />);
  let user = await open();
  await fillPairing(user);
  await user.click(screen.getByRole("button", { name: "Create pairing" }));
  await screen.findByText("Operation awaiting confirmation");
  expect(writes).toHaveLength(1);
  const saved = sessionStorage.getItem(journalKey("admin"));
  expect(saved).toContain("future-product");
  view.unmount();
  render(<SuiteAdministration actorId="admin" />);
  user = await open();
  await user.click(screen.getByRole("button", { name: "Retry same Suite request" }));
  await screen.findByLabelText("Setup information");
  expect(writes).toHaveLength(2);
  expect(writes[1].headers).toEqual(writes[0].headers);
  expect(writes[1].body).toEqual(writes[0].body);
  expect(sessionStorage.getItem(journalKey("admin"))).toBeNull();
  expect(sessionStorage.getItem(recoveryKey("admin"))).not.toContain(
    "secret-result-only-in-memory",
  );
  expect((screen.getByLabelText("Setup information") as HTMLTextAreaElement).value).toContain(
    "secret-result-only-in-memory",
  );
  await user.click(screen.getByRole("button", { name: "Clear setup information" }));
  expect(screen.queryByLabelText("Setup information")).toBeNull();
});

test("sensitive proofs and service credentials never enter the recovery journal", async () => {
  const request = {
    path: "/api/nox/pairings/p/complete",
    method: "POST" as const,
    body: '{"proof":"private-hmac"}',
    privateBody: true,
    credential: "noxsvc_private",
    message: "Done",
  };
  const pending = await preparePending("admin", request);
  const stored = sessionStorage.getItem(journalKey("admin"))!;
  expect(stored).not.toContain("private-hmac");
  expect(stored).not.toContain("noxsvc_private");
  expect(pending.needsBody).toBe(true);
  expect(pending.needsCredential).toBe(true);
  expect(readPending("another-user")).toBeNull();
  expect(await requestFingerprint(request)).toBe(pending.fingerprint);
  expect(await requestFingerprint({ ...request, credential: "changed" })).not.toBe(
    pending.fingerprint,
  );
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
  await fillPairing(user);
  await user.click(screen.getByRole("button", { name: "Create pairing" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("Trust configuration changed");
  expect((screen.getByLabelText("Product ID", { exact: true }) as HTMLInputElement).value).toBe(
    "future-product",
  );
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
    connection: { baseUrl: "https://future.example.com" },
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
  expect(screen.queryByRole("button", { name: "Create pairing" })).toBeNull();
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

test("saved private requests need the exact original secret to retry", async () => {
  const request = {
    path: "/api/nox/pairings/p/ack",
    method: "POST" as const,
    credential: "original-credential",
    message: "Acknowledged",
  };
  await preparePending("admin", request);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(state)));
  render(<SuiteAdministration actorId="admin" />);
  const user = await open();
  await user.type(screen.getByLabelText("Original service credential"), "wrong-credential");
  await user.click(screen.getByRole("button", { name: "Retry same Suite request" }));
  await screen.findByText(/This is not the original request/);
  expect(readPending("admin")).not.toBeNull();
  expect(screen.getByRole("button", { name: "Create pairing" }).matches(":disabled")).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Refresh Suite state" }));
});

test("a confirmed setup response can be recovered after reload without storing its secret", async () => {
  const request = {
    path: "/api/nox/pairings",
    method: "POST" as const,
    headers: { "If-None-Match": "*" },
    body: JSON.stringify({
      contractVersion: "1.0",
      productId: "future-product",
      purpose: "product",
      baseUrl: "https://future.example.com",
    }),
    secretResult: true,
    message: "Pairing recovered.",
  };
  const original = await preparePending("admin", request);
  sessionStorage.setItem(recoveryKey("admin"), JSON.stringify(original));
  sessionStorage.removeItem(journalKey("admin"));
  const writes: RequestInit[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((_path: string, options?: RequestInit) => {
      if (options?.method) {
        writes.push(options);
        return Promise.resolve(response({ secret: "recovered-secret" }));
      }
      return Promise.resolve(response(state));
    }),
  );
  render(<SuiteAdministration actorId="admin" />);
  const user = await open();
  await user.click(screen.getByRole("button", { name: "Recover last setup response" }));
  await screen.findByLabelText("Setup information");
  expect((writes[0].headers as Record<string, string>)["Idempotency-Key"]).toBe(original.key);
  expect(sessionStorage.getItem(recoveryKey("admin"))).not.toContain("recovered-secret");
});

test("local trust management remains available without a target network policy", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      response({
        ...state,
        configuration: {
          ...state.configuration,
          canEnroll: false,
          enrollmentReason: "Configure approved target networks before enrollment.",
        },
      }),
    ),
  );
  render(<SuiteAdministration actorId="admin" />);
  await open();
  expect(screen.getByRole("button", { name: "Create pairing" }).matches(":disabled")).toBe(true);
  expect(screen.getByRole("button", { name: "Prepare signing key" }).matches(":disabled")).toBe(
    false,
  );
});

test("a malformed successful snapshot fails only the Suite pane", async () => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response({ ok: true })));
  render(<SuiteAdministration actorId="admin" />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Manage Suite integrations" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("alert").textContent).toContain("Invalid Suite administration response");
  expect(screen.queryByRole("button", { name: "Create pairing" })).toBeNull();
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
  await expect(
    sendSuiteRequest(
      {
        path: "/api/nox/pairings/p/ack",
        method: "POST",
        credential: "noxsvc_secret",
        message: "Done",
      },
      "key",
    ),
  ).rejects.toMatchObject({ uncertain: false });
  expect(fetcher).not.toHaveBeenCalled();
});
