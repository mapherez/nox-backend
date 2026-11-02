import { afterEach, expect, test, vi } from "vite-plus/test";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Connections } from "./Connections";
import { MutationContext } from "./MutationForm";
import type { FoundationConnection } from "./model";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const connection: FoundationConnection = {
  service: "yard",
  origin: "http://yard.internal:8095",
  revision: "config-one",
  updatedAt: "",
  status: "not_paired",
};
test("saved HTTP address exposes verification and does not claim integration", async () => {
  const perform = vi.fn().mockResolvedValue(undefined);
  render(
    <MutationContext.Provider value={{ busy: false, perform }}>
      <Connections connections={[connection]} />
    </MutationContext.Provider>,
  );
  expect((screen.getByLabelText("Yard address") as HTMLInputElement).value).toBe(connection.origin);
  expect(screen.queryByRole("button", { name: "Connect Yard" })).toBeNull();
  fireEvent.submit(screen.getByRole("button", { name: "Verify Yard" }).closest("form")!);
  await vi.waitFor(() =>
    expect(perform).toHaveBeenCalledWith(
      "/api/admin/connections/yard/verify",
      "POST",
      { revision: "config-one" },
      {},
      expect.any(String),
    ),
  );
});
test("verified identity is confirmed without exposing protocol secrets", async () => {
  const perform = vi.fn().mockResolvedValue(undefined);
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(
    <MutationContext.Provider value={{ busy: false, perform }}>
      <Connections
        connections={[
          {
            ...connection,
            setup: {
              attemptId: "attempt-one",
              state: "verified",
              productId: "nox-yard",
              displayName: "NoX Yard",
              productVersion: "test",
              canResume: true,
              canVerify: true,
              canCancel: true,
            },
          },
        ]}
      />
    </MutationContext.Provider>,
  );
  fireEvent.submit(screen.getByRole("button", { name: "Connect Yard" }).closest("form")!);
  await vi.waitFor(() =>
    expect(perform).toHaveBeenCalledWith(
      "/api/admin/connections/yard/connect",
      "POST",
      { attemptId: "attempt-one" },
      {},
      expect.any(String),
    ),
  );
  expect(window.confirm).toHaveBeenCalledWith("Connect NoX Yard test to this Backend?");
  expect(screen.queryByLabelText("Setup information")).toBeNull();
});
test("a pending connection offers resume and cancellation before editing its URL", () => {
  render(
    <Connections
      connections={[
        {
          ...connection,
          status: "pairing_pending",
          setup: {
            attemptId: "attempt-one",
            state: "waiting",
            productId: "nox-yard",
            displayName: "NoX Yard",
            productVersion: "test",
            canResume: true,
            canVerify: false,
            canCancel: true,
          },
        },
      ]}
    />,
  );
  expect(screen.getByRole("button", { name: "Resume Yard connection" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Cancel Yard connection" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Save Yard configuration" }).matches(":disabled")).toBe(
    true,
  );
  expect(screen.queryByRole("button", { name: "Remove Yard configuration" })).toBeNull();
});
