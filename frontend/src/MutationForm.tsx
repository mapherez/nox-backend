import { createContext, useContext, useState } from "react";
import type { ReactNode, SubmitEvent } from "react";

export const MutationContext = createContext<{
  busy: boolean;
  perform: (
    path: string,
    method: string,
    body: Record<string, string> | undefined,
    headers: Record<string, string>,
    message: string,
  ) => Promise<void>;
}>({
  busy: false,
  perform: async () => {
    throw Error("Administration is unavailable.");
  },
});

export async function requestJSON<T>(path: string, options: RequestInit = {}): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      ...options,
    });
  } catch (failure) {
    if (failure instanceof DOMException && failure.name === "AbortError") throw failure;
    throw Error("Request could not be confirmed. Refresh the dashboard before retrying a change.");
  }
  if (!response.ok) {
    let message =
      response.status === 401
        ? "Your session has expired. Refresh and sign in again."
        : "The administration request failed.";
    try {
      const error = (await response.json()) as { message?: string };
      if (response.status !== 401 && typeof error.message === "string") message = error.message;
    } catch {
      /* Keep the status-based diagnostic for non-JSON proxy errors. */
    }
    throw Error(message);
  }
  return (await response.json()) as T;
}

export function MutationForm({
  path,
  method = "POST",
  headers = {},
  message,
  children,
  className,
  confirm,
  onSuccess,
}: {
  path: string;
  method?: string;
  headers?: Record<string, string>;
  message: string;
  children: ReactNode;
  className?: string;
  confirm?: () => boolean;
  onSuccess?: () => void;
}) {
  const { busy, perform } = useContext(MutationContext);
  const [error, setError] = useState("");
  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (busy || (confirm && !confirm())) return;
    const form = event.currentTarget;
    const body = Object.fromEntries(
      Array.from(new FormData(form).entries(), ([key, value]) => [
        key,
        typeof value === "string" ? value : value.name,
      ]),
    );
    setError("");
    try {
      await perform(path, method, Object.keys(body).length ? body : undefined, headers, message);
      form.reset();
      onSuccess?.();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Administration failed.");
    }
  }
  return (
    <form
      action={path}
      method="post"
      className={className}
      onSubmit={(event) => void submit(event)}
      aria-busy={busy}
    >
      <fieldset disabled={busy}>{children}</fieldset>
      {error && (
        <p className="error-message" role="alert">
          {error}
        </p>
      )}
    </form>
  );
}
