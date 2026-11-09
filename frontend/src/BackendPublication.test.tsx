import { afterEach, expect, test, vi } from "vite-plus/test";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackendPublication } from "./BackendPublication";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});
const response = (value: unknown, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => value,
});

test("dashboard publishes with the existing session and no credential/body", async () => {
  const fetcher = vi.fn().mockResolvedValue(response({ enabled: true }));
  vi.stubGlobal("fetch", fetcher);
  const view = render(<BackendPublication actorId="owner" />);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  const [path, options] = fetcher.mock.calls[0];
  expect(path).toBe("/api/admin/suite/backend/publish");
  expect(options).toMatchObject({
    method: "POST",
    credentials: "same-origin",
    headers: { "X-Nox-Request": "1" },
  });
  expect(options.body).toBeUndefined();
  expect(options.headers.Authorization).toBeUndefined();
  view.rerender(<BackendPublication actorId="owner" />);
  expect(fetcher).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("alert")).toBeNull();
});

test("a failure is visible and only explicit retry submits again", async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValueOnce(response({ message: "Association was revoked." }, 409))
    .mockResolvedValueOnce(response({ enabled: true }));
  vi.stubGlobal("fetch", fetcher);
  render(<BackendPublication actorId="owner" />);
  await screen.findByRole("alert");
  expect(fetcher).toHaveBeenCalledTimes(1);
  await userEvent
    .setup()
    .click(screen.getByRole("button", { name: "Retry Backend tool publication" }));
  await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  expect(fetcher).toHaveBeenCalledTimes(2);
});

test("disabled Suite does not show an error or ask for setup", async () => {
  const fetcher = vi.fn().mockResolvedValue(response({ enabled: false }));
  vi.stubGlobal("fetch", fetcher);
  render(<BackendPublication actorId="owner" />);
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.queryByRole("button")).toBeNull();
});
