// Only request metadata/non-secret payloads survive a reload. Pairing proofs,
// provisioning responses and Bearer credentials are never stored in the browser.
export interface SuiteRequest {
  path: string;
  method: "POST" | "PUT" | "DELETE";
  headers?: Record<string, string>;
  body?: string;
  credential?: string;
  privateBody?: boolean;
  secretResult?: boolean;
  message: string;
}
export interface PendingSuiteRequest {
  key: string;
  fingerprint: string;
  request: SuiteRequest;
  needsBody: boolean;
  needsCredential: boolean;
}
export const journalKey = (actorId: string) => `nox-suite-operation:${actorId}`;
export const recoveryKey = (actorId: string) => `nox-suite-recovery:${actorId}`;
export function readPending(actorId: string, recovery = false): PendingSuiteRequest | null {
  const value = sessionStorage.getItem(recovery ? recoveryKey(actorId) : journalKey(actorId));
  if (!value) return null;
  const pending = JSON.parse(value) as PendingSuiteRequest;
  if (
    !pending.key ||
    !pending.fingerprint ||
    !pending.request.path.startsWith("/api/nox/") ||
    !["POST", "PUT", "DELETE"].includes(pending.request.method)
  )
    throw Error(
      "The saved Suite operation cannot be read. Reconcile its state before clearing this tab's operation record.",
    );
  return pending;
}
export async function requestFingerprint(request: SuiteRequest): Promise<string> {
  const bytes = new TextEncoder().encode(
    JSON.stringify([
      request.method,
      request.path,
      request.headers ?? {},
      request.body ?? null,
      request.credential ?? null,
    ]),
  );
  return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
}
export async function preparePending(
  actorId: string,
  request: SuiteRequest,
): Promise<PendingSuiteRequest> {
  const pending = {
    key: crypto.randomUUID(),
    fingerprint: await requestFingerprint(request),
    request: {
      ...request,
      body: request.privateBody ? undefined : request.body,
      credential: undefined,
    },
    needsBody: Boolean(request.privateBody && request.body),
    needsCredential: Boolean(request.credential),
  };
  sessionStorage.setItem(journalKey(actorId), JSON.stringify(pending));
  return pending;
}
export class SuiteRequestFailure extends Error {
  constructor(
    message: string,
    readonly uncertain: boolean,
  ) {
    super(message);
  }
}
export async function sendSuiteRequest(request: SuiteRequest, key: string): Promise<unknown> {
  let response: Response;
  try {
    response = await fetch(request.path, {
      method: request.method,
      credentials: "same-origin",
      cache: "no-store",
      redirect: "error",
      signal: AbortSignal.timeout(25000),
      headers: {
        "X-Nox-Request": "1",
        "Idempotency-Key": key,
        ...(request.body ? { "Content-Type": "application/json" } : {}),
        ...request.headers,
        ...(request.credential ? { Authorization: `Bearer ${request.credential}` } : {}),
      },
      ...(request.body ? { body: request.body } : {}),
    });
  } catch {
    throw new SuiteRequestFailure(
      "The outcome could not be confirmed. Reconcile state or retry this exact request with its saved operation key.",
      true,
    );
  }
  if (response.status === 204 && response.ok) return null;
  let value: unknown;
  try {
    value = await response.json();
  } catch {
    throw new SuiteRequestFailure(
      "The response could not be confirmed. Reconcile before issuing another operation.",
      true,
    );
  }
  if (!response.ok) {
    const failure = value as { message?: string; error?: { message?: string } };
    throw new SuiteRequestFailure(
      failure.error?.message ?? failure.message ?? "The Suite operation failed.",
      response.status >= 500 || response.status === 408,
    );
  }
  return value;
}
