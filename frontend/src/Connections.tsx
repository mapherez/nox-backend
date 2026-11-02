import type { FoundationConnection } from "./model";
import { MutationForm } from "./MutationForm";

function Connection({ connection }: { connection: FoundationConnection }) {
  const name = connection.service === "yard" ? "Yard" : "Gateway";
  const path = `/api/admin/connections/${connection.service}`;
  const setup = connection.setup;
  const pending = Boolean(setup?.canResume && setup.state !== "verified");
  const headers: Record<string, string> = connection.revision
    ? { "If-Match": `"${connection.revision}"` }
    : { "If-None-Match": "*" };
  return (
    <div className="connection-card">
      <h3>{name}</h3>
      <p className="muted">
        {
          {
            not_configured: "Not configured",
            not_paired: "Configuration saved · Awaiting pairing",
            pairing_pending: "Pairing pending · Awaiting service confirmation",
            paired: "Service paired · Registration pending",
            registered: "Service registered · Availability tracked separately",
            recovery_required: "Service credential recovery required",
          }[connection.status]
        }
      </p>
      <MutationForm
        path={path}
        method="PUT"
        headers={headers}
        message={`${name} configuration saved.`}
      >
        <fieldset disabled={pending}>
          <div className="form-row">
            <div className="field grow">
              <label htmlFor={`origin-${connection.service}`}>{name} address</label>
              <input
                id={`origin-${connection.service}`}
                name="origin"
                type="url"
                defaultValue={connection.origin}
                required
                placeholder={`http://${connection.service}.internal`}
                autoComplete="off"
              />
            </div>
            <button type="submit">Save {name} configuration</button>
          </div>
        </fieldset>
      </MutationForm>
      {connection.revision && !pending && (
        <MutationForm
          path={path}
          method="DELETE"
          headers={headers}
          message={`${name} connection configuration removed.`}
          confirm={() => window.confirm(`Remove the saved ${name} address?`)}
        >
          <button className="secondary" type="submit">
            Remove {name} configuration
          </button>
        </MutationForm>
      )}
      {connection.revision && connection.status !== "registered" && (!setup || setup.canVerify) && (
        <MutationForm
          path={`${path}/verify`}
          message={`${name} identity verified. Confirm to connect.`}
        >
          <input type="hidden" name="revision" value={connection.revision} />
          <button type="submit">Verify {name}</button>
        </MutationForm>
      )}
      {setup && (
        <div className="suite-notice" role="status">
          <p>
            {setup.displayName} {setup.productVersion} · {setup.state}
          </p>
          {setup.message && <p>{setup.message}</p>}
          {setup.canResume && (
            <MutationForm
              path={`${path}/connect`}
              message={`${name} connection processed. Check its registration state.`}
              confirm={() =>
                setup.state !== "verified" ||
                window.confirm(
                  `Connect ${setup.displayName} ${setup.productVersion} to this Backend?`,
                )
              }
            >
              <input type="hidden" name="attemptId" value={setup.attemptId} />
              <button type="submit">
                {setup.state === "verified" ? `Connect ${name}` : `Resume ${name} connection`}
              </button>
            </MutationForm>
          )}
          {setup.canCancel && (
            <MutationForm
              path={`${path}/cancel`}
              message={`${name} pending connection cancelled.`}
              confirm={() => window.confirm(`Cancel the pending ${name} connection?`)}
            >
              <input type="hidden" name="attemptId" value={setup.attemptId} />
              <button className="secondary" type="submit">
                Cancel {name} connection
              </button>
            </MutationForm>
          )}
        </div>
      )}
    </div>
  );
}
export function Connections({ connections }: { connections: FoundationConnection[] }) {
  return (
    <section aria-labelledby="connections-title">
      <p className="eyebrow">SUITE CONFIGURATION</p>
      <h2 id="connections-title">Service connections</h2>
      <p className="muted">
        Save the HTTP or HTTPS address used to open Yard and Gateway. An IP address with a port or a
        dedicated hostname is supported, without an application path.
      </p>
      <p className="muted">
        After saving, verify the service and confirm the connection here. Both services must run on
        the same host as Backend. Credentials are exchanged automatically. MCP availability is
        tracked separately.
      </p>
      {connections.map((connection) => (
        <Connection key={`${connection.service}:${connection.revision}`} connection={connection} />
      ))}
    </section>
  );
}
