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
    },
    {
      id: "m",
      email: "member@example.com",
      firstName: "",
      displayName: "Member",
      role: "USER",
      status: "ACTIVE",
      lastLoginAt: "",
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
test("admin UI preserves native routes, exact revisions, encoded download IDs and safe text", () => {
  render(<DashboardView data={data} />);
  expect(screen.getByText("r9223372036854775807")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Download vault" }).getAttribute("href")).toBe(
    "/vault-dashboard/vaults/download?vaultId=v%2Fa",
  );
  expect(screen.getByText("Notes <script>")).toBeTruthy();
  expect(document.querySelector("script")).toBeNull();
  expect(screen.getByRole("button", { name: "Add user" }).closest("form")?.action).toContain(
    "/vault-dashboard/users/add",
  );
  expect((screen.getByLabelText("API key") as HTMLInputElement).type).toBe("password");
});
test("member UI retains own credentials and vaults without administrative controls", () => {
  render(
    <DashboardView
      data={{ ...data, isAdmin: false, user: { ...data.user, role: "USER" }, users: null }}
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
test("API key rotation still requires confirmation before submitting the native form", () => {
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
