import type { FoundationConnection } from "./model";
import { MutationForm } from "./MutationForm";

function Connection({ connection }: { connection: FoundationConnection }) {
  const name = connection.service === "yard" ? "Yard" : "Gateway";
  const path = `/api/admin/connections/${connection.service}`;
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
        <div className="form-row">
          <div className="field grow">
            <label htmlFor={`origin-${connection.service}`}>{name} HTTPS address</label>
            <input
              id={`origin-${connection.service}`}
              name="origin"
              type="url"
              defaultValue={connection.origin}
              required
              placeholder={`https://${connection.service}.example.com`}
              autoComplete="off"
            />
          </div>
          <button type="submit">Save {name} configuration</button>
        </div>
      </MutationForm>
      {connection.revision && (
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
    </div>
  );
}
export function Connections({ connections }: { connections: FoundationConnection[] }) {
  return (
    <section aria-labelledby="connections-title">
      <p className="eyebrow">SUITE CONFIGURATION</p>
      <h2 id="connections-title">Service connections</h2>
      <p className="muted">
        Save the approved HTTPS address for Yard and Gateway. An IP address with a port or a
        dedicated hostname is supported, without an application path.
      </p>
      <p className="muted">
        Saving an address does not contact or trust the service. Use Suite integration management
        below for pairing and verified registration. MCP readiness is reported separately.
      </p>
      {connections.map((connection) => (
        <Connection key={`${connection.service}:${connection.revision}`} connection={connection} />
      ))}
    </section>
  );
}
