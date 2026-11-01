import { createContext, useContext, useEffect, useId, useRef, useState } from "react";
import type { ReactNode, SubmitEvent } from "react";
import { requestJSON } from "./MutationForm";
import { formatTime } from "./model";
import { parseSuiteAdministration } from "./suite-model";
import type {
  SuiteAdministration as Administration,
  SuiteProduct,
  SuiteService,
} from "./suite-model";
import {
  journalKey,
  recoveryKey,
  preparePending,
  readPending,
  requestFingerprint,
  sendSuiteRequest,
  SuiteRequestFailure,
} from "./suite-requests";
import type { PendingSuiteRequest, SuiteRequest } from "./suite-requests";

const encoded = encodeURIComponent;
const condition = (revision?: string): Record<string, string> =>
  revision ? { "If-Match": JSON.stringify(revision) } : { "If-None-Match": "*" };
const body = (value: unknown) => JSON.stringify(value);
const SuiteContext = createContext<{
  blocked: boolean;
  canEnroll: boolean;
  perform: (request: SuiteRequest) => Promise<void>;
}>({
  blocked: true,
  canEnroll: false,
  perform: async () => {
    throw Error("Suite administration is unavailable.");
  },
});
function Field({
  label,
  name,
  type = "text",
  value,
  required = true,
  children,
}: {
  label: string;
  name: string;
  type?: string;
  value?: string;
  required?: boolean;
  children?: ReactNode;
}) {
  const id = useId();
  return (
    <div className="field grow">
      <label htmlFor={id}>{label}</label>
      {children ? (
        <select id={id} name={name} defaultValue={value}>
          {children}
        </select>
      ) : (
        <input
          id={id}
          name={name}
          type={type}
          defaultValue={value}
          required={required}
          autoComplete="off"
          spellCheck={false}
        />
      )}
    </div>
  );
}
function SuiteForm({
  label,
  build,
  children,
  confirm,
  enrollment = false,
}: {
  label: string;
  build: (data: FormData) => SuiteRequest;
  children?: ReactNode;
  confirm?: string;
  enrollment?: boolean;
}) {
  const { blocked: globallyBlocked, perform, canEnroll } = useContext(SuiteContext);
  const blocked = globallyBlocked || (enrollment && !canEnroll);
  const [error, setError] = useState("");
  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault();
    if (blocked || (confirm && !window.confirm(confirm))) return;
    const form = event.currentTarget;
    setError("");
    try {
      await perform(build(new FormData(form)));
      form.reset();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "The operation failed.");
    }
  }
  return (
    <form onSubmit={(event) => void submit(event)} aria-label={label}>
      <fieldset disabled={blocked}>
        <div className="form-row">
          {children}
          <button type="submit">{label}</button>
        </div>
      </fieldset>
      {error && (
        <p role="alert" className="error-message">
          {error}
        </p>
      )}
    </form>
  );
}
const text = (data: FormData, name: string) => {
  const value = data.get(name);
  return typeof value === "string" ? value : "";
};
function SecretResult({ value, onClose }: { value: unknown; onClose: () => void }) {
  const id = useId();
  const [error, setError] = useState("");
  const raw = JSON.stringify(value, null, 2);
  async function copy() {
    try {
      await navigator.clipboard.writeText(raw);
    } catch {
      setError("Clipboard unavailable. Select the information and copy it manually.");
    }
  }
  return (
    <aside className="suite-notice" role="region" aria-label="Sensitive setup information">
      <h3>Save this setup information</h3>
      <p>
        Transfer it securely to the intended product. The product must save its credential before
        acknowledgment. Recovery closes after acknowledgment or expiry.
      </p>
      <label htmlFor={id}>Setup information</label>
      <textarea id={id} rows={10} readOnly value={raw} autoComplete="off" spellCheck={false} />
      <div className="form-row">
        <button type="button" onClick={() => void copy()}>
          Copy setup information
        </button>
        <button type="button" className="secondary" onClick={onClose}>
          Clear setup information
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
    </aside>
  );
}
function Service({ service }: { service: SuiteService }) {
  const base = `/api/nox/services/${encoded(service.serviceId)}`;
  const current = service.credentials.find((credential) => credential.current);
  return (
    <details className="suite-card">
      <summary>
        {service.productId} · {service.purpose} · {service.state}
        {service.current ? " · current association" : " · awaiting acknowledgment"}
      </summary>
      <p className="mono">{service.baseUrl}</p>
      {service.state === "active" && (
        <SuiteForm
          label={`Revoke service ${service.productId}`}
          confirm={`Revoke ${service.productId}'s service credentials? Its registration is retained and will need reconciliation.`}
          build={() => ({
            path: base,
            method: "DELETE",
            headers: condition(service.revision),
            message: "Service revoked. Its registry membership is retained.",
          })}
        />
      )}
      {service.state === "active" && current && current.state !== "provisioned" && (
        <SuiteForm
          label={`Rotate credential for ${service.productId}`}
          build={() => ({
            path: `${base}/credentials`,
            method: "POST",
            headers: condition(current.revision),
            body: body({ contractVersion: "1.0", credentialRevision: current.revision }),
            secretResult: true,
            message: "Replacement credential created. Save it in the product, then acknowledge it.",
          })}
        />
      )}
      <h4>Credential generations</h4>
      {service.credentials.map((credential) => (
        <div key={credential.credentialId} className="suite-row">
          <p>
            <span className="mono">{credential.credentialId}</span> · {credential.state}
            {credential.current ? " · current" : ""}
            {credential.expiresAt ? ` · expires ${formatTime(credential.expiresAt)}` : ""}
          </p>
          {credential.current &&
            credential.state === "provisioned" &&
            service.state === "active" && (
              <SuiteForm
                label={`Acknowledge replacement for ${service.productId}`}
                confirm="Confirm that the product has securely saved this replacement credential. Acknowledgment revokes its predecessor and closes recovery."
                build={(data) => ({
                  path: `${base}/credentials/${encoded(credential.credentialId)}/ack`,
                  method: "POST",
                  credential: text(data, "credential"),
                  message: "Replacement acknowledged; predecessor revoked.",
                })}
              >
                <Field label="Saved replacement credential" name="credential" type="password" />
              </SuiteForm>
            )}
          {credential.state !== "revoked" && service.state === "active" && (
            <SuiteForm
              label={`Revoke credential ${credential.credentialId}`}
              confirm="Revoke this credential generation? Existing grants derived from it will stop being admitted."
              build={() => ({
                path: `${base}/credentials/${encoded(credential.credentialId)}`,
                method: "DELETE",
                headers: condition(credential.revision),
                message: "Credential generation revoked.",
              })}
            />
          )}
        </div>
      ))}
      {service.purpose === "installer" && service.state === "active" && (
        <>
          <h4>Authorized deployments</h4>
          {service.deployments.map((deployment) => (
            <div key={deployment.deploymentId} className="suite-row">
              <p>
                {deployment.deploymentId} · {deployment.productId} · {deployment.baseUrl}
              </p>
              <SuiteForm
                label={`Remove deployment grant ${deployment.deploymentId}`}
                confirm="Remove this installer's authorization for this deployment? Existing membership is retained."
                build={() => ({
                  path: `${base}/deployments/${encoded(deployment.deploymentId)}`,
                  method: "DELETE",
                  headers: condition(deployment.revision),
                  message: "Deployment grant removed; membership retained.",
                })}
              />
            </div>
          ))}
          <SuiteForm
            enrollment
            label={`Authorize deployment for ${service.productId}`}
            build={(data) => {
              const id = text(data, "deployment");
              const existing = service.deployments.find((item) => item.deploymentId === id);
              return {
                path: `${base}/deployments/${encoded(id)}`,
                method: "PUT",
                headers: condition(existing?.revision),
                body: body({ productId: text(data, "product"), baseUrl: text(data, "origin") }),
                message: "Installer deployment authorization saved.",
              };
            }}
          >
            <Field label="Deployment ID" name="deployment" />
            <Field label="Deployment product ID" name="product" />
            <Field label="Deployment HTTPS address" name="origin" type="url" />
          </SuiteForm>
        </>
      )}
    </details>
  );
}
function Product({
  product,
  data,
  now,
}: {
  product: SuiteProduct;
  data: Administration;
  now: number;
}) {
  const path = `/api/nox/products/${encoded(product.productId)}`;
  const trusted = data.state.services.some(
    (service) =>
      service.current &&
      service.state === "active" &&
      service.associationId === product.associationId &&
      service.credentials.some(
        (credential) =>
          ["active", "overlap"].includes(credential.state) &&
          (!credential.expiresAt || Date.parse(credential.expiresAt) - 5000 > now),
      ),
  );
  const blocked = data.state.blockedProducts.includes(product.productId);
  return (
    <article className="suite-card">
      <h4>
        {product.declaration.displayName} <span className="mono">{product.productId}</span>
      </h4>
      <p>
        {blocked
          ? "Registered · incompatible declaration blocks Suite authorization"
          : !trusted
            ? "Registered · trust unavailable; reconcile association"
            : product.declaration.capabilities.suiteMcp
              ? "Registered · Suite MCP declared"
              : "Registered · Suite MCP not advertised"}
      </p>
      <p className="mono">{product.connection.baseUrl}</p>
      <p>
        {["yard", "gateway"]
          .map((source) => {
            const observation = data.state.observations.find(
              (item) =>
                item.observation.productId === product.productId &&
                item.observation.source === source,
            );
            const state =
              observation && Date.parse(observation.freshUntil) > now
                ? observation.observation.state
                : "unknown";
            return `${source === "yard" ? "Lifecycle" : "MCP readiness"}: ${state}`;
          })
          .join(" · ")}
      </p>
      <SuiteForm
        enrollment
        label={`Verify and update ${product.productId}`}
        build={(fields) => ({
          path,
          method: "PUT",
          headers: condition(product.revision),
          body: body({
            contractVersion: "1.0",
            baseUrl: text(fields, "origin"),
            ...(product.managedBy ? { managedBy: product.managedBy } : {}),
          }),
          message: "Product declaration verified and registration updated.",
        })}
      >
        <Field
          label={`HTTPS address for ${product.productId}`}
          name="origin"
          type="url"
          value={product.connection.baseUrl}
        />
      </SuiteForm>
      <SuiteForm
        label={`Remove registration ${product.productId}`}
        confirm="Remove this registration and retire its association? This does not uninstall the product or delete its data."
        build={() => ({
          path,
          method: "DELETE",
          headers: condition(product.revision),
          message: "Registration removed and its association retired.",
        })}
      />
    </article>
  );
}
function SuitePanel({ actorId }: { actorId: string }) {
  const [data, setData] = useState<Administration | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refreshNeeded, setRefreshNeeded] = useState(false);
  const [secret, setSecret] = useState<unknown>(null);
  const [pending, setPending] = useState<PendingSuiteRequest | null>(null);
  const [recovery, setRecovery] = useState<PendingSuiteRequest | null>(null);
  const [journalError, setJournalError] = useState("");
  const [now, setNow] = useState(Date.now());
  const inFlight = useRef(false);
  const mounted = useRef(true);
  async function reload(signal?: AbortSignal) {
    try {
      const value = parseSuiteAdministration(
        await requestJSON<unknown>("/api/admin/suite", { signal }),
      );
      // A TLS upstream does not make an HTTP browser connection secure.
      setData(
        window.location.protocol === "https:"
          ? value
          : {
              ...value,
              configuration: {
                ...value.configuration,
                canManage: false,
                canEnroll: false,
                reason:
                  "Open this Backend through its trusted HTTPS address to manage Suite integration.",
              },
            },
      );
      setError("");
      setRefreshNeeded(false);
    } catch (failure) {
      if (signal?.aborted) return;
      setError(failure instanceof Error ? failure.message : "Suite state is unavailable.");
      setRefreshNeeded(true);
      throw failure;
    }
  }
  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    try {
      setPending(readPending(actorId));
      setRecovery(readPending(actorId, true));
    } catch (failure) {
      setJournalError(String(failure));
    }
    void reload(controller.signal).catch(() => {});
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => {
      mounted.current = false;
      controller.abort();
      clearInterval(timer);
    };
  }, [actorId]);
  function clearPending() {
    sessionStorage.removeItem(journalKey(actorId));
    setPending(null);
  }
  async function execute(request: SuiteRequest, retry?: PendingSuiteRequest) {
    if (inFlight.current) throw Error("Wait for the current operation.");
    if (!data?.configuration.canManage || refreshNeeded || journalError)
      throw Error("Refresh and resolve Suite configuration before changing integration.");
    if (pending && !retry)
      throw Error("Reconcile or retry the saved operation before starting another.");
    inFlight.current = true;
    setBusy(true);
    setMessage("");
    setSecret(null);
    try {
      if (retry && (await requestFingerprint(request)) !== retry.fingerprint)
        throw Error(
          "This is not the original request. Supply the exact original proof or credential, or reconcile the saved operation.",
        );
      const operation = retry ?? (await preparePending(actorId, request));
      setPending(operation);
      sessionStorage.setItem(journalKey(actorId), JSON.stringify(operation));
      try {
        const result = await sendSuiteRequest(request, operation.key);
        if (!mounted.current)
          throw new SuiteRequestFailure(
            "The response arrived after this view closed. Recover the saved operation.",
            true,
          );
        if (request.secretResult) {
          sessionStorage.setItem(recoveryKey(actorId), JSON.stringify(operation));
          setRecovery(operation);
          setSecret(result);
        }
        clearPending();
        setMessage(request.message);
      } catch (failure) {
        if (failure instanceof SuiteRequestFailure && !failure.uncertain) {
          clearPending();
          await reload().catch(() => {});
        }
        throw failure;
      }
      await reload().catch(() => {
        setMessage(`${request.message} State refresh failed; refresh before another change.`);
      });
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  async function retry(event: SubmitEvent<HTMLFormElement>, saved = pending) {
    event.preventDefault();
    if (!saved) return;
    const fields = new FormData(event.currentTarget);
    try {
      await execute(
        {
          ...saved.request,
          ...(saved.needsBody ? { body: text(fields, "originalBody") } : {}),
          ...(saved.needsCredential ? { credential: text(fields, "originalCredential") } : {}),
        },
        saved,
      );
    } catch (failure) {
      setMessage(failure instanceof Error ? failure.message : "The saved request failed.");
    }
  }
  async function reconcile() {
    if (
      !window.confirm(
        "Read the actual state and close this saved operation? If setup information was lost, retry the same operation to recover it before closing.",
      )
    )
      return;
    try {
      await reload();
      clearPending();
      if (journalError) {
        sessionStorage.removeItem(recoveryKey(actorId));
        setRecovery(null);
      }
      setJournalError("");
      setMessage("State reconciled. The saved operation record was closed.");
    } catch {
      /* Keep the pending operation. */
    }
  }
  const blocked =
    busy ||
    Boolean(pending) ||
    refreshNeeded ||
    Boolean(journalError) ||
    !data?.configuration.canManage;
  return (
    <SuiteContext.Provider
      value={{ blocked, canEnroll: data?.configuration.canEnroll ?? false, perform: execute }}
    >
      <div className="suite-console">
        <div className="section-heading">
          <h3>Suite administration</h3>
          <button
            className="secondary"
            type="button"
            disabled={busy}
            onClick={() => void reload().catch(() => {})}
          >
            Refresh Suite state
          </button>
        </div>
        {error && (
          <p role="alert" className="error-message">
            Suite state unavailable: {error} Native account and vault controls remain available.
          </p>
        )}
        {journalError && (
          <aside>
            <p role="alert" className="error-message">
              {journalError}
            </p>
            <button type="button" disabled={busy} onClick={() => void reconcile()}>
              Reconcile and clear unreadable operation
            </button>
          </aside>
        )}
        {busy && <p role="status">Applying Suite operation…</p>}
        {message && <p role="status">{message}</p>}
        {pending && (
          <aside className="suite-notice">
            <h4>Operation awaiting confirmation</h4>
            <p className="mono">
              {pending.request.method} {pending.request.path}
            </p>
            <p>
              Reconcile actual state or retry this exact request. A retry keeps the original
              operation key and preconditions.
            </p>
            <form onSubmit={(event) => void retry(event)}>
              <fieldset disabled={busy || refreshNeeded || !data?.configuration.canManage}>
                {pending.needsBody && (
                  <label>
                    Original request JSON
                    <textarea
                      name="originalBody"
                      rows={5}
                      required
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </label>
                )}
                {pending.needsCredential && (
                  <Field
                    label="Original service credential"
                    name="originalCredential"
                    type="password"
                  />
                )}
                <button type="submit">Retry same Suite request</button>
              </fieldset>
            </form>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => void reconcile()}
            >
              Reconcile and close operation
            </button>
          </aside>
        )}
        {recovery && !pending && secret === null && (
          <aside className="suite-notice">
            <h4>Last setup response</h4>
            <p>
              Recover the last setup response using its original operation key. Recovery is
              available only until acknowledgment or expiry; no secret is stored in this tab.
            </p>
            <form onSubmit={(event) => void retry(event, recovery)}>
              <fieldset disabled={blocked}>
                {recovery.needsBody && (
                  <label>
                    Original setup request JSON
                    <textarea
                      name="originalBody"
                      rows={4}
                      required
                      autoComplete="off"
                      spellCheck={false}
                    />
                  </label>
                )}
                {recovery.needsCredential && (
                  <Field
                    label="Original setup credential"
                    name="originalCredential"
                    type="password"
                  />
                )}
                <button type="submit">Recover last setup response</button>
              </fieldset>
            </form>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => {
                sessionStorage.removeItem(recoveryKey(actorId));
                setRecovery(null);
              }}
            >
              Forget setup recovery
            </button>
          </aside>
        )}
        {secret !== null && <SecretResult value={secret} onClose={() => setSecret(null)} />}
        {data && (
          <>
            <p className="muted">
              State read {formatTime(data.state.observedAt)}. Availability is an observation;
              registration and permission are separate.
            </p>
            {!data.configuration.canManage && (
              <p role="status" className="suite-notice">
                {data.configuration.reason}
              </p>
            )}
            {data.configuration.canManage && data.configuration.enrollmentReason && (
              <p className="suite-notice" role="status">
                {data.configuration.enrollmentReason}
              </p>
            )}
            <p>
              Direct MCP: {data.configuration.directMcpEnabled ? "enabled" : "disabled"} · Suite
              MCP: {data.configuration.suiteMcpEnabled ? "enabled" : "disabled"}
            </p>
            <h4>Start product pairing</h4>
            <p>
              Choose any product ID. Gateway and installer are technical service purposes. Transfer
              the setup information to the product; it confirms possession and saves its credential
              before acknowledgment.
            </p>
            <SuiteForm
              enrollment
              label="Create pairing"
              build={(fields) => {
                const productId = text(fields, "productId");
                const current = data.state.services.find(
                  (service) => service.productId === productId && service.current,
                );
                const installer = text(fields, "installer");
                if (
                  current &&
                  !window.confirm(
                    `Replace the trusted association for ${productId}? The old association stays active until the new service acknowledges.`,
                  )
                )
                  throw Error("Replacement cancelled.");
                return {
                  path: "/api/nox/pairings",
                  method: "POST",
                  headers: condition(current?.headRevision),
                  body: body({
                    contractVersion: "1.0",
                    productId,
                    baseUrl: text(fields, "baseUrl"),
                    purpose: text(fields, "purpose"),
                    ...(installer
                      ? {
                          managedBy: {
                            productId: installer,
                            deploymentId: text(fields, "deploymentId"),
                          },
                        }
                      : {}),
                  }),
                  secretResult: true,
                  message:
                    "Pairing created. Transfer its setup information; membership is not yet registered.",
                };
              }}
            >
              <Field label="Product ID" name="productId" />
              <Field label="Product HTTPS address" name="baseUrl" type="url" />
              <Field label="Service purpose" name="purpose" value="product">
                <option value="product">Product</option>
                <option value="gateway">Gateway</option>
                <option value="installer">Installer</option>
              </Field>
              <Field
                label="Managing installer product ID (optional)"
                name="installer"
                required={false}
              />
              <Field label="Deployment ID (if managed)" name="deploymentId" required={false} />
            </SuiteForm>
            <h4>Pairings</h4>
            {data.state.pairings.length === 0 && <p>No pairing operations.</p>}
            {data.state.pairings.map((pairing) => (
              <details key={pairing.pairingId} className="suite-card">
                <summary>
                  {pairing.productId} ·{" "}
                  {Date.parse(pairing.expiresAt) - 5000 <= now &&
                  ["pending", "provisioned"].includes(pairing.state)
                    ? "expired · refresh state"
                    : pairing.state}
                </summary>
                <p className="mono">{pairing.baseUrl}</p>
                <p>
                  {pairing.purpose} · expires {formatTime(pairing.expiresAt)}
                </p>
                {["pending", "provisioned"].includes(pairing.state) && (
                  <>
                    <SuiteForm
                      enrollment
                      label={`Complete pairing for ${pairing.productId}`}
                      build={(fields) => ({
                        path: `/api/nox/pairings/${encoded(pairing.pairingId)}/complete`,
                        method: "POST",
                        body: text(fields, "proof"),
                        privateBody: true,
                        secretResult: true,
                        message:
                          "Proof verified; transfer provisioning to the product and save it before acknowledgment.",
                      })}
                    >
                      <label>
                        Product pairing proof JSON
                        <textarea
                          name="proof"
                          rows={4}
                          required
                          autoComplete="off"
                          spellCheck={false}
                        />
                      </label>
                    </SuiteForm>
                    {pairing.state === "provisioned" && (
                      <SuiteForm
                        label={`Acknowledge saved provisioning for ${pairing.productId}`}
                        confirm="Confirm that the intended product has securely saved its provisioning. This closes secret recovery."
                        build={(fields) => ({
                          path: `/api/nox/pairings/${encoded(pairing.pairingId)}/ack`,
                          method: "POST",
                          credential: text(fields, "credential"),
                          message:
                            "Service acknowledged. Verify and register its product separately.",
                        })}
                      >
                        <Field
                          label="Saved product service credential"
                          name="credential"
                          type="password"
                        />
                      </SuiteForm>
                    )}
                    <SuiteForm
                      label={`Cancel pairing for ${pairing.productId}`}
                      confirm="Cancel this pairing and revoke any unacknowledged provisioning?"
                      build={() => ({
                        path: `/api/nox/pairings/${encoded(pairing.pairingId)}`,
                        method: "DELETE",
                        headers: condition(pairing.revision),
                        message: "Pairing cancelled; unacknowledged credentials revoked.",
                      })}
                    />
                  </>
                )}
              </details>
            ))}
            <h4>Trusted services</h4>
            {data.state.services.length === 0 && <p>No trusted service associations.</p>}
            {data.state.services.map((service) => (
              <Service
                key={`${service.serviceId}:${service.revision}:${service.credentials.map((item) => item.state).join()}`}
                service={service}
              />
            ))}
            <h4>Register a paired product</h4>
            <p>
              Registration verifies the actual declaration over the proven HTTPS connection. It does
              not depend on Gateway readiness.
            </p>
            <SuiteForm
              enrollment
              label="Verify and register product"
              build={(fields) => {
                const id = text(fields, "product");
                if (data.state.products.some((product) => product.productId === id))
                  throw Error("Use the existing product's verified update control.");
                const installer = text(fields, "installer");
                return {
                  path: `/api/nox/products/${encoded(id)}`,
                  method: "PUT",
                  headers: condition(),
                  body: body({
                    contractVersion: "1.0",
                    baseUrl: text(fields, "origin"),
                    ...(installer
                      ? {
                          managedBy: {
                            productId: installer,
                            deploymentId: text(fields, "deployment"),
                          },
                        }
                      : {}),
                  }),
                  message: "Product verified and registered. Availability is reported separately.",
                };
              }}
            >
              <Field label="Paired product ID" name="product" />
              <Field label="Paired product HTTPS address" name="origin" type="url" />
              <Field
                label="Registration installer product ID (optional)"
                name="installer"
                required={false}
              />
              <Field
                label="Registration deployment ID (if managed)"
                name="deployment"
                required={false}
              />
            </SuiteForm>
            {data.state.products.map((product) => (
              <Product
                key={`${product.productId}:${product.revision}`}
                product={product}
                data={data}
                now={now}
              />
            ))}
            <h4>Issuer signing keys</h4>
            <p className="mono">{data.state.issuer}</p>
            <SuiteForm
              label="Prepare signing key"
              build={() => ({
                path: "/api/nox/keys",
                method: "POST",
                headers: condition(data.state.keyRevision),
                message: "Signing key prepared. It is published before activation.",
              })}
            />
            {data.state.signingKeys.map((key) => (
              <div className="suite-card" key={key.keyId}>
                <p className="mono">
                  {key.keyId} · {key.state}
                  {key.retiresAt ? ` · retires ${formatTime(key.retiresAt)}` : ""}
                </p>
                {key.state === "prepared" && (
                  <SuiteForm
                    label={`Activate signing key ${key.keyId}`}
                    confirm="Activate the prepared key? The former signing key remains available for verification during its retirement window."
                    build={() => ({
                      path: `/api/nox/keys/${encoded(key.keyId)}/activate`,
                      method: "POST",
                      headers: condition(data.state.keyRevision),
                      message: "Prepared signing key activated.",
                    })}
                  />
                )}{" "}
                {key.state !== "revoked" && (
                  <SuiteForm
                    label={`Revoke signing key ${key.keyId}`}
                    confirm="Revoke this signing key immediately? Grants signed with it stop being admitted; revoking the active key stops issuance until another key is activated."
                    build={() => ({
                      path: `/api/nox/keys/${encoded(key.keyId)}`,
                      method: "DELETE",
                      headers: condition(data.state.keyRevision),
                      message: "Signing key revoked.",
                    })}
                  />
                )}
              </div>
            ))}
          </>
        )}
      </div>
    </SuiteContext.Provider>
  );
}
export function SuiteAdministration({ actorId }: { actorId: string }) {
  const [open, setOpen] = useState(false);
  return (
    <section aria-label="Suite integration management">
      <h2>Suite integration management</h2>
      <p className="muted">
        Pair services, verify products and manage trust independently of Gateway or Portal.
      </p>
      {!open ? (
        <button type="button" onClick={() => setOpen(true)}>
          Manage Suite integrations
        </button>
      ) : (
        <SuitePanel key={actorId} actorId={actorId} />
      )}
    </section>
  );
}
