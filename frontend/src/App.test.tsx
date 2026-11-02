import { afterEach, expect, test, vi } from "vite-plus/test";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App, DashboardView } from "./App";
import type { Dashboard } from "./model";
const data: Dashboard = {
  authenticated: true,
  oauthConfigured: true,
  loginUrl: "/auth/google/start?redirect=/vault-dashboard",
  serverUrl: "https://backend.example.com",
  apiKey: "noxsync_example",
  user: {
    id: "u",
    email: "admin@example.com",
    firstName: "Ada",
    displayName: "Ada",
    role: "ADMIN",
    status: "ACTIVE",
    lastLoginAt: "",
    isAdminManager: true,
    permissions: { canDelete: false, canChangeRole: false, canChangeStatus: false },
  },
  isAdmin: true,
  users: [
    {
      id: "u",
      email: "admin@example.com",
      firstName: "Ada",
      displayName: "Ada",
      role: "ADMIN",
      status: "ACTIVE",
      lastLoginAt: "",
      isAdminManager: true,
      permissions: { canDelete: false, canChangeRole: false, canChangeStatus: false },
    },
    {
      id: "m",
      email: "member@example.com",
      firstName: "",
      displayName: "Member",
      role: "USER",
      status: "ACTIVE",
      lastLoginAt: "",
      isAdminManager: false,
      permissions: { canDelete: true, canChangeRole: true, canChangeStatus: true },
    },
  ],
  vaults: [
    {
      vaultId: "v/a",
      name: "Notes <script>",
      revision: "9223372036854775807",
      sizeBytes: "1024",
      updatedAt: "2026-10-30T10:00:00Z",
      status: "ACTIVE",
    },
  ],
  deletedVaults: [],
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
test("anonymous view keeps Google login and configuration guidance without exposing account controls", () => {
  const { rerender } = render(<DashboardView data={{ ...data, authenticated: false }} />);
  expect(screen.getByRole("link", { name: "Continue with Google" }).getAttribute("href")).toBe(
    data.loginUrl,
  );
  expect(screen.queryByLabelText("API key")).toBeNull();
  rerender(<DashboardView data={{ ...data, authenticated: false, oauthConfigured: false }} />);
  expect(screen.getByText(/Google OAuth is not configured/)).toBeTruthy();
});
test("admin UI uses API routes and preserves exact revisions, encoded download IDs and safe text", () => {
  render(<DashboardView data={data} />);
  expect(screen.getByText("r9223372036854775807")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Download vault" }).getAttribute("href")).toBe(
    "/api/vaults/v%2Fa/download",
  );
  expect(screen.getByText("Notes <script>")).toBeTruthy();
  expect(document.querySelector("script")).toBeNull();
  expect(screen.getByRole("button", { name: "Add user" }).closest("form")?.action).toContain(
    "/api/admin/users",
  );
  expect((screen.getByLabelText("API key") as HTMLInputElement).type).toBe("password");
});
test("member UI retains own credentials and vaults without administrative controls", () => {
  render(
    <DashboardView
      data={{
        ...data,
        isAdmin: false,
        user: { ...data.user, role: "USER", isAdminManager: false },
        users: null,
      }}
    />,
  );
  expect(screen.getByLabelText("API key")).toBeTruthy();
  expect(screen.queryByText("User management")).toBeNull();
  expect(screen.queryByRole("button", { name: "Add user" })).toBeNull();
});
test("key visibility and clipboard fallback work without clearing the real key", async () => {
  const user = userEvent.setup();
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockRejectedValue(Error("Unavailable")) },
  });
  render(<DashboardView data={data} />);
  await user.click(screen.getByRole("button", { name: "Copy API key" }));
  const input = screen.getByLabelText("API key") as HTMLInputElement;
  expect(input.type).toBe("text");
  expect(input.value).toBe(data.apiKey);
  expect(document.activeElement).toBe(input);
  expect(screen.getByRole("status").textContent).toContain("Clipboard unavailable");
  await user.click(screen.getByRole("button", { name: "Hide API key" }));
  expect(input.type).toBe("password");
});
test("API key rotation requires confirmation before calling its API", () => {
  vi.spyOn(window, "confirm").mockReturnValue(false);
  render(<DashboardView data={data} />);
  const form = screen.getByRole("button", { name: "Generate new API key" }).closest("form")!;
  expect(fireEvent.submit(form)).toBe(false);
  expect(window.confirm).toHaveBeenCalledOnce();
});
test("loading failures are visible and retry fetches same-origin session data without caching", async () => {
  const fetchMock = vi
    .fn()
    .mockRejectedValueOnce(Error("offline"))
    .mockResolvedValueOnce({ ok: true, json: async () => ({ ...data, authenticated: false }) });
  vi.stubGlobal("fetch", fetchMock);
  render(<App />);
  expect(await screen.findByRole("alert")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await waitFor(() =>
    expect(screen.getByRole("link", { name: "Continue with Google" })).toBeTruthy(),
  );
  expect(fetchMock).toHaveBeenLastCalledWith(
    "/api/dashboard",
    expect.objectContaining({ credentials: "same-origin", cache: "no-store" }),
  );
});

test("user changes call the JSON API with cookie CSRF protection and refresh the view", async () => {
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
  vi.stubGlobal("fetch", fetchMock);
  const reload = vi.fn().mockResolvedValue(undefined);
  render(<DashboardView data={data} reload={reload} />);
  fireEvent.change(screen.getByLabelText("Google account email"), {
    target: { value: "new@example.com" },
  });
  fireEvent.submit(screen.getByRole("button", { name: "Add user" }).closest("form")!);
  await waitFor(() => expect(reload).toHaveBeenCalledOnce());
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/admin/users",
    expect.objectContaining({
      method: "POST",
      credentials: "same-origin",
      redirect: "error",
      headers: { "X-Nox-Request": "1", "Content-Type": "application/json" },
      body: JSON.stringify({ email: "new@example.com", role: "USER" }),
    }),
  );
  expect((screen.getByLabelText("Google account email") as HTMLInputElement).value).toBe("");
});

test("API errors remain visible beside the form and preserve unsaved input", async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: false,
    status: 403,
    json: async () => ({ message: "Admin access is required." }),
  });
  vi.stubGlobal("fetch", fetchMock);
  const reload = vi.fn();
  render(<DashboardView data={data} reload={reload} />);
  fireEvent.change(screen.getByLabelText("Google account email"), {
    target: { value: "new@example.com" },
  });
  fireEvent.submit(screen.getByRole("button", { name: "Add user" }).closest("form")!);
  expect((await screen.findByRole("alert")).textContent).toContain("Admin access is required.");
  expect((screen.getByLabelText("Google account email") as HTMLInputElement).value).toBe(
    "new@example.com",
  );
  expect(reload).not.toHaveBeenCalled();
});

test("an unconfirmed response is not replayed and reports the need to refresh", async () => {
  const fetchMock = vi.fn().mockRejectedValue(Error("connection reset"));
  vi.stubGlobal("fetch", fetchMock);
  render(<DashboardView data={data} />);
  fireEvent.submit(screen.getByRole("button", { name: "Add user" }).closest("form")!);
  expect((await screen.findByRole("alert")).textContent).toContain(
    "Refresh the dashboard before retrying",
  );
  expect(fetchMock).toHaveBeenCalledOnce();
});

test("a saved mutation with a failed refresh blocks further changes without repeating the write", async () => {
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ ok: true }) });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <DashboardView
      data={data}
      reload={async () => {
        throw Error("offline");
      }}
    />,
  );
  fireEvent.submit(screen.getByRole("button", { name: "Add user" }).closest("form")!);
  expect((await screen.findByRole("alert")).textContent).toContain("The change was saved");
  await waitFor(() =>
    expect(
      (screen.getByRole("button", { name: "Add user" }) as HTMLButtonElement).closest("fieldset")
        ?.disabled,
    ).toBe(true),
  );
  expect(fetchMock).toHaveBeenCalledOnce();
});

test("connection configuration uses revision preconditions and never claims to be connected", async () => {
  const fetchMock = vi.fn().mockResolvedValue({
    ok: false,
    status: 412,
    json: async () => ({ message: "Configuration changed. Reload before saving." }),
  });
  vi.stubGlobal("fetch", fetchMock);
  render(
    <DashboardView
      data={{
        ...data,
        connections: [
          {
            service: "yard",
            origin: "https://yard.example.com",
            revision: "connection_old",
            updatedAt: "",
            status: "not_paired",
          },
          { service: "gateway", origin: "", revision: "", updatedAt: "", status: "not_configured" },
        ],
      }}
    />,
  );
  expect(screen.getByText("Configuration saved · Awaiting pairing")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Yard address"), {
    target: { value: "https://yard-new.example.com" },
  });
  fireEvent.submit(
    screen.getByRole("button", { name: "Save Yard configuration" }).closest("form")!,
  );
  expect((await screen.findByRole("alert")).textContent).toContain("Configuration changed");
  expect(fetchMock).toHaveBeenCalledWith(
    "/api/admin/connections/yard",
    expect.objectContaining({
      method: "PUT",
      headers: expect.objectContaining({ "If-Match": '"connection_old"' }),
      body: JSON.stringify({ origin: "https://yard-new.example.com" }),
    }),
  );
  expect((screen.getByLabelText("Yard address") as HTMLInputElement).value).toBe(
    "https://yard-new.example.com",
  );
});

test("a manager can manage a regular admin while its configured account remains protected", () => {
  const secondary = { ...data.users![1]!, role: "ADMIN" };
  render(<DashboardView data={{ ...data, users: [data.users![0]!, secondary] }} />);
  const managerRow = screen.getByRole("row", { name: /admin@example.com/ });
  const adminRow = screen.getByRole("row", { name: /member@example.com/ });
  expect(
    (managerRow.querySelector('button[aria-label="Delete user"]') as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(
    (adminRow.querySelector('button[aria-label="Delete user"]') as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(screen.getByRole("button", { name: "Make member" }).closest("form")?.action).toContain(
    "/api/admin/users/m/role",
  );
  expect(
    (adminRow.querySelector('button[aria-label="Disable user"]') as HTMLButtonElement).disabled,
  ).toBe(false);
  expect(managerRow.textContent).toContain("Admin Manager");
});

test("ordinary admins obey backend capabilities instead of enabling all admin actions", () => {
  const restricted = {
    ...data.users![1]!,
    role: "ADMIN",
    permissions: { canDelete: false, canChangeRole: false, canChangeStatus: false },
  };
  render(
    <DashboardView
      data={{ ...data, user: { ...data.user, isAdminManager: false }, users: [restricted] }}
    />,
  );
  expect((screen.getByRole("button", { name: "Delete user" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect((screen.getByRole("button", { name: "Disable user" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect(screen.queryByRole("button", { name: "Make member" })).toBeNull();
});

test("an older Backend without management capabilities does not break the independent frontend", () => {
  render(
    <DashboardView
      data={{ ...data, users: data.users!.map((user) => ({ ...user, permissions: undefined })) }}
    />,
  );
  expect(screen.getByRole("alert").textContent).toContain("Update the Backend service");
  expect((screen.getByRole("button", { name: "Add user" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect(screen.getByRole("link", { name: "Download vault" })).toBeTruthy();
  for (const button of screen.getAllByRole("button", { name: "Delete user" }))
    expect((button as HTMLButtonElement).disabled).toBe(true);
});

test("foundation trust states do not claim registry or MCP readiness", () => {
  const connection = {
    service: "gateway" as const,
    origin: "https://gateway.example.com",
    revision: "connection_current",
    updatedAt: "",
  };
  const { rerender } = render(
    <DashboardView data={{ ...data, connections: [{ ...connection, status: "paired" }] }} />,
  );
  expect(screen.getByText("Service paired · Registration pending")).toBeTruthy();
  rerender(
    <DashboardView data={{ ...data, connections: [{ ...connection, status: "registered" }] }} />,
  );
  expect(screen.getByText("Service registered · Availability tracked separately")).toBeTruthy();
  rerender(
    <DashboardView
      data={{ ...data, connections: [{ ...connection, status: "recovery_required" }] }}
    />,
  );
  expect(screen.getByText("Service credential recovery required")).toBeTruthy();
  rerender(
    <DashboardView
      data={{ ...data, connections: [{ ...connection, status: "pairing_pending" }] }}
    />,
  );
  expect(screen.getByText("Pairing pending · Awaiting service confirmation")).toBeTruthy();
});
