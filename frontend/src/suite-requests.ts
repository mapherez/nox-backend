import { sha256 } from "@noble/hashes/sha2.js";

// Browser operations contain only non-secret administration data. Pairing and
// service credentials are delivered and persisted by local participants.
export interface SuiteRequest {
  path: string;
  method: "POST" | "PUT" | "DELETE";
  headers?: Record<string, string>;
  body?: string;
  message: string;
}
export interface PendingSuiteRequest {
  key: string;
  fingerprint: string;
  request: SuiteRequest;
}
export const journalKey = (actorId: string) => `nox-suite-operation:${actorId}`;
// Read/clear old records, never replay their manual secret-transfer operations.
export const recoveryKey = (actorId: string) => `nox-suite-recovery:${actorId}`;
export class SuiteRequestFailure extends Error {
  constructor(
    message: string,
    readonly uncertain: boolean,
  ) {
    super(message);
  }
}
function validateRequest(request: SuiteRequest): void {
  const allowed =
    (request.method === "POST" &&
      /^\/api\/nox\/keys(?:\/[^/?#]+\/activate)?$/.test(request.path)) ||
    (request.method === "DELETE" &&
      /^\/api\/nox\/(?:keys|products|pairings|services)\/[^/?#]+$/.test(request.path)) ||
    (["PUT", "DELETE"].includes(request.method) &&
      /^\/api\/nox\/services\/[^/?#]+\/deployments\/[^/?#]+$/.test(request.path)) ||
    (request.method === "DELETE" &&
      /^\/api\/nox\/services\/[^/?#]+\/credentials\/[^/?#]+$/.test(request.path)) ||
    (request.method === "PUT" && /^\/api\/nox\/products\/[^/?#]+$/.test(request.path));
  const fields = ["path", "method", "headers", "body", "message"];
  if (!allowed || Object.keys(request).some((field) => !fields.includes(field)))
    throw new SuiteRequestFailure(
      "This operation is not available through browser administration. Use Service connections or the local installer to manage pairing and service credentials.",
      false,
    );
}
export function readPending(actorId: string): PendingSuiteRequest | null {
  if (sessionStorage.getItem(recoveryKey(actorId)))
    throw Error(
      "A previous manual setup record exists. Reconcile the current state; resume setup through Service connections or the local installer.",
    );
  const value = sessionStorage.getItem(journalKey(actorId));
  if (!value) return null;
  const pending = JSON.parse(value) as PendingSuiteRequest & {
    needsBody?: boolean;
    needsCredential?: boolean;
  };
  if (
    !pending.key ||
    !pending.fingerprint ||
    !pending.request ||
    pending.needsBody ||
    pending.needsCredential
  )
    throw Error(
      "The saved Suite operation cannot be retried. Reconcile the current state before clearing its record.",
    );
  validateRequest(pending.request);
  return { key: pending.key, fingerprint: pending.fingerprint, request: pending.request };
}
export async function requestFingerprint(request: SuiteRequest): Promise<string> {
  validateRequest(request);
  const bytes = new TextEncoder().encode(
    JSON.stringify([
      request.method,
      request.path,
      request.headers ?? {},
      request.body ?? null,
      // Preserve fingerprints of non-secret operations already saved in a tab.
      null,
    ]),
  );
  return Array.from(sha256(bytes), (byte) => byte.toString(16).padStart(2, "0")).join("");
}
export async function preparePending(
  actorId: string,
  request: SuiteRequest,
): Promise<PendingSuiteRequest> {
  validateRequest(request);
  const pending = { key: operationID(), fingerprint: await requestFingerprint(request), request };
  sessionStorage.setItem(journalKey(actorId), JSON.stringify(pending));
  return pending;
}
export async function sendSuiteRequest(request: SuiteRequest, key: string): Promise<unknown> {
  validateRequest(request);
  let response: Response;
  try {
    response = await fetch(
      `/api/admin/suite/operations/${request.path.slice("/api/nox/".length)}`,
      {
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
        },
        ...(request.body ? { body: request.body } : {}),
      },
    );
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

// getRandomValues is available in HTTP browsers; randomUUID requires a secure context.
function operationID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6]! & 15) | 64;
  bytes[8] = (bytes[8]! & 63) | 128;
  const h = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
