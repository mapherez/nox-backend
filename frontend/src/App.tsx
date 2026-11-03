import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { MutationContext, MutationForm, requestJSON } from "./MutationForm";
import { Connections } from "./Connections";
import { SuiteAdministration } from "./SuiteAdministration";
import type { Dashboard, User, Vault } from "./model";
import { formatBytes, formatTime, messages } from "./model";

function Icon({ name }: { name: string }) {
  const paths: Record<string, ReactNode> = {
    vault: (
      <>
        <rect x="3" y="3" width="18" height="18" rx="4" />
        <circle cx="12" cy="12" r="4" />
        <path d="M12 8v8m-4-4h8M18 7v3" />
      </>
    ),
    copy: (
      <>
        <rect x="9" y="9" width="13" height="13" rx="2" />
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
      </>
    ),
    eye: (
      <>
        <path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7S2 12 2 12Z" />
        <circle cx="12" cy="12" r="3" />
      </>
    ),
    download: <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" />,
    trash: (
      <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6M10 11v6M14 11v6" />
    ),
    refresh: (
      <path d="M21 12a9 9 0 0 0-15-6.7L3 8M3 3v5h5M3 12a9 9 0 0 0 15 6.7L21 16M21 21v-5h-5" />
    ),
    restore: <path d="M3 12a9 9 0 1 0 3-6.7M3 3v6h6M12 7v5l3 2" />,
    logout: <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" />,
    shield: <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10M9 12l2 2 4-4" />,
    user: (
      <>
        <path d="M20 21a8 8 0 0 0-16 0" />
        <circle cx="12" cy="7" r="4" />
      </>
    ),
    "user-x": (
      <>
        <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M17 8l5 5M22 8l-5 5" />
        <circle cx="9" cy="7" r="4" />
      </>
    ),
    x: <path d="M18 6L6 18M6 6l12 12" />,
  };
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      {paths[name]}
    </svg>
  );
}
function Action({
  label,
  icon,
  onClick,
  disabled = false,
  danger = false,
}: {
  label: string;
  icon: string;
  onClick?: () => void;
  disabled?: boolean;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      className={`icon-button ${danger ? "danger" : "secondary"}`}
      title={label}
      aria-label={label}
      onClick={onClick}
      disabled={disabled}
    >
      <Icon name={icon} />
    </button>
  );
}
function Hidden({ name, value }: { name: string; value: string }) {
  return <input type="hidden" name={name} value={value} />;
}
function Dialog({
  title,
  children,
  onClose,
}: {
  title: string;
  children: ReactNode;
  onClose: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    ref.current?.showModal();
  }, []);
  return (
    <dialog ref={ref} aria-label={title} onCancel={onClose}>
      <h2>{title}</h2>
      {children}
      <div className="dialog-actions">
        <button type="button" className="secondary" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
function Credentials({ data }: { data: Dashboard }) {
  const [visible, setVisible] = useState(false);
  const [feedback, setFeedback] = useState("");
  const keyRef = useRef<HTMLInputElement>(null);
  const urlRef = useRef<HTMLInputElement>(null);
  async function copy(value: string, kind: "key" | "url") {
    try {
      await navigator.clipboard.writeText(value);
      setFeedback(kind === "key" ? "API key copied" : "Server URL copied");
    } catch {
      if (kind === "key") setVisible(true);
      const input = kind === "key" ? keyRef.current : urlRef.current;
      input?.focus();
      input?.select();
      setFeedback(
        "Clipboard unavailable. The value is selected; copy it using your keyboard or the selection menu.",
      );
    }
  }
  function rotate() {
    return window.confirm(
      "Generate a new API key? Apps using the current key will stop connecting until you update them.",
    );
  }
  return (
    <>
      <div id="copy-feedback" className="sr-only" role="status" aria-live="polite">
        {feedback}
      </div>
      <div className="section-intro">
        <h2>Connect your apps</h2>
        <p className="muted">Use the same server URL and API key in each client.</p>
      </div>
      <div className="grid">
        <section>
          <label className="field-heading" htmlFor="server-url">
            Server URL
          </label>
          <div className="copy-row">
            <input
              ref={urlRef}
              id="server-url"
              className="mono"
              spellCheck={false}
              readOnly
              value={data.serverUrl}
            />
            <Action
              label="Copy server URL"
              icon="copy"
              onClick={() => void copy(data.serverUrl, "url")}
            />
          </div>
          <p className="muted">The base address of your backend, without /v1.</p>
        </section>
        <section>
          <label className="field-heading" htmlFor="api-key">
            API key
          </label>
          <div className="copy-row">
            <input
              ref={keyRef}
              id="api-key"
              className="mono"
              type={visible ? "text" : "password"}
              autoComplete="off"
              spellCheck={false}
              readOnly
              value={data.apiKey}
            />
            <button
              className="icon-button secondary"
              type="button"
              aria-label={visible ? "Hide API key" : "Show API key"}
              aria-pressed={visible}
              aria-controls="api-key"
              onClick={() => setVisible(!visible)}
            >
              <Icon name="eye" />
            </button>
            <Action
              label="Copy API key"
              icon="copy"
              onClick={() => void copy(data.apiKey, "key")}
            />
          </div>
          <MutationForm
            path="/api/account/api-key/rotate"
            className="key-actions"
            confirm={rotate}
            message={messages["api-key-rotated"]!}
          >
            <button className="danger" type="submit">
              Generate new API key
            </button>
          </MutationForm>
          <p className="muted">Generating a new key disconnects apps using the previous key.</p>
        </section>
      </div>
    </>
  );
}
function Vaults({ vaults, deleted }: { vaults: Vault[]; deleted: Vault[] }) {
  const [selected, setSelected] = useState<Vault | null>(null);
  const [restoring, setRestoring] = useState(false);
  const [purge, setPurge] = useState<Vault | null>(null);
  return (
    <section>
      <div className="section-heading">
        <div>
          <p className="eyebrow">STORAGE</p>
          <h2>
            Your vaults <span className="count">{vaults.length}</span>
          </h2>
        </div>
        <Action
          label="Restore deleted vaults"
          icon="restore"
          disabled={!deleted.length}
          onClick={() => setRestoring(true)}
        />
      </div>
      {vaults.length ? (
        <div className="table-wrapper" role="region" tabIndex={0} aria-label="Your vaults">
          <table>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">Revision</th>
                <th scope="col">Updated</th>
                <th scope="col">Size</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {vaults.map((vault) => (
                <tr key={vault.vaultId}>
                  <th scope="row" className="vault-name">
                    <Icon name="vault" />
                    <span>{vault.name}</span>
                  </th>
                  <td>
                    <span className="badge mono">r{vault.revision}</span>
                  </td>
                  <td>
                    <time dateTime={vault.updatedAt}>{formatTime(vault.updatedAt)}</time>
                  </td>
                  <td>{formatBytes(vault.sizeBytes)}</td>
                  <td className="action-cell">
                    <a
                      className="icon-button secondary"
                      href={`/api/vaults/${encodeURIComponent(vault.vaultId)}/download`}
                      title="Download vault"
                      aria-label="Download vault"
                    >
                      <Icon name="download" />
                    </a>
                    <Action
                      label="Delete vault"
                      icon="trash"
                      danger
                      onClick={() => setSelected(vault)}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="empty-state">
          No vaults yet. Create your first vault from a connected app, such as NoX Sync.
        </p>
      )}
      {selected && (
        <Dialog title="Delete vault" onClose={() => setSelected(null)}>
          <p>
            Delete &quot;{selected.name}&quot;? This vault will be hidden and blocked from future
            sync.
          </p>
          <MutationForm
            path={`/api/vaults/${encodeURIComponent(selected.vaultId)}`}
            method="DELETE"
            message={messages["vault-deleted"]!}
            onSuccess={() => setSelected(null)}
          >
            <button className="danger" type="submit">
              Delete vault
            </button>
          </MutationForm>
        </Dialog>
      )}
      {restoring && (
        <Dialog title="Deleted vaults" onClose={() => setRestoring(false)}>
          <table>
            <tbody>
              {deleted.map((vault) => (
                <tr key={vault.vaultId}>
                  <th scope="row">{vault.name}</th>
                  <td>
                    <MutationForm
                      path={`/api/vaults/${encodeURIComponent(vault.vaultId)}/restore`}
                      message={messages["vault-restored"]!}
                      onSuccess={() => setRestoring(false)}
                    >
                      <button className="secondary" type="submit">
                        Restore vault
                      </button>
                    </MutationForm>
                  </td>
                  <td>
                    <Action
                      label="Permanently delete vault"
                      icon="trash"
                      danger
                      onClick={() => {
                        setRestoring(false);
                        setPurge(vault);
                      }}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </Dialog>
      )}
      {purge && (
        <Dialog title="Permanently delete vault" onClose={() => setPurge(null)}>
          <p>Permanently delete &quot;{purge.name}&quot;? This cannot be restored.</p>
          <MutationForm
            path={`/api/vaults/${encodeURIComponent(purge.vaultId)}/purge`}
            method="DELETE"
            message={messages["vault-purged"]!}
            onSuccess={() => setPurge(null)}
          >
            <button className="danger" type="submit">
              Permanently delete vault
            </button>
          </MutationForm>
        </Dialog>
      )}
    </section>
  );
}
function Users({ users }: { users: User[] }) {
  const [selected, setSelected] = useState<User | null>(null);
  return (
    <section>
      <div className="section-heading">
        <div>
          <p className="eyebrow">ADMINISTRATION</p>
          <h2>
            User management <span className="count">{users.length}</span>
          </h2>
        </div>
      </div>
      <p className="muted">
        Manage Backend roles and data here. Login access is configured in NoX Auth.
      </p>
      {users.some((user) => !user.permissions) && (
        <p role="alert" className="error-message">
          Update the Backend service to manage accounts with this frontend. Your vaults remain
          available.
        </p>
      )}
      <MutationForm
        path="/api/admin/users"
        className="add-user-panel"
        message={messages["user-saved"]!}
      >
        <div className="form-row">
          <div className="field grow">
            <label htmlFor="new-email">Google account email</label>
            <input
              id="new-email"
              name="email"
              type="email"
              autoComplete="email"
              required
              placeholder="person@example.com"
            />
          </div>
          <div className="field">
            <label htmlFor="new-role">Role</label>
            <select id="new-role" name="role" defaultValue="USER">
              <option value="USER">Member</option>
              <option value="ADMIN">Admin</option>
            </select>
          </div>
          <button type="submit" disabled={users.some((user) => !user.permissions)}>
            Add user
          </button>
        </div>
      </MutationForm>
      <div className="table-wrapper" role="region" tabIndex={0} aria-label="Users">
        <table>
          <thead>
            <tr>
              <th scope="col">Email</th>
              <th scope="col">Role</th>
              <th scope="col">Status</th>
              <th scope="col">Actions</th>
            </tr>
          </thead>
          <tbody>
            {users.map((user) => (
              <tr key={user.id}>
                <th scope="row">{user.email}</th>
                <td>
                  <span className="badge">
                    {user.isAdminManager
                      ? "Admin Manager"
                      : user.role === "ADMIN"
                        ? "Admin"
                        : "Member"}
                  </span>
                </td>
                <td>
                  <MutationForm
                    path={`/api/admin/users/${encodeURIComponent(user.id)}/status`}
                    method="PUT"
                    message={messages["user-saved"]!}
                  >
                    <Hidden
                      name="status"
                      value={user.status === "ACTIVE" ? "DISABLED" : "ACTIVE"}
                    />
                    <button
                      className={`status-toggle ${user.status === "ACTIVE" ? "is-active" : ""}`}
                      type="submit"
                      title={user.status === "ACTIVE" ? "Disable user" : "Enable user"}
                      aria-label={user.status === "ACTIVE" ? "Disable user" : "Enable user"}
                      disabled={!user.permissions?.canChangeStatus}
                    >
                      <span />
                    </button>
                  </MutationForm>
                </td>
                <td className="action-cell">
                  {!user.permissions?.canChangeRole ? (
                    <Action label="This account's role is protected" icon="user" disabled />
                  ) : (
                    <MutationForm
                      path={`/api/admin/users/${encodeURIComponent(user.id)}/role`}
                      method="PUT"
                      message={messages["user-saved"]!}
                    >
                      <Hidden name="role" value={user.role === "ADMIN" ? "USER" : "ADMIN"} />
                      <button
                        className="icon-button secondary"
                        type="submit"
                        title={user.role === "ADMIN" ? "Make member" : "Make admin"}
                        aria-label={user.role === "ADMIN" ? "Make member" : "Make admin"}
                      >
                        <Icon name={user.role === "ADMIN" ? "user" : "shield"} />
                      </button>
                    </MutationForm>
                  )}
                  <Action
                    label="Delete user"
                    icon="user-x"
                    danger
                    disabled={!user.permissions?.canDelete}
                    onClick={() => setSelected(user)}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {selected && (
        <Dialog title="Delete user" onClose={() => setSelected(null)}>
          <p>Delete &quot;{selected.email}&quot; and all owned sync data? This cannot be undone.</p>
          <MutationForm
            path={`/api/admin/users/${encodeURIComponent(selected.id)}`}
            method="DELETE"
            message={messages["user-deleted"]!}
            onSuccess={() => setSelected(null)}
          >
            <button className="danger" type="submit">
              Delete user
            </button>
          </MutationForm>
        </Dialog>
      )}
    </section>
  );
}
export function DashboardView({ data, reload }: { data: Dashboard; reload?: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [refreshError, setRefreshError] = useState("");
  const inFlight = useRef(false);
  async function perform(
    path: string,
    method: string,
    body: Record<string, string> | undefined,
    headers: Record<string, string>,
    successMessage: string,
  ) {
    if (inFlight.current) throw Error("Wait for the current change to finish.");
    inFlight.current = true;
    setBusy(true);
    setRefreshError("");
    try {
      await requestJSON(path, {
        method,
        headers: {
          "X-Nox-Request": "1",
          ...(body ? { "Content-Type": "application/json" } : {}),
          ...headers,
        },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      setMessage(successMessage);
      try {
        await reload?.();
      } catch {
        setRefreshError(
          "The change was saved, but the dashboard could not be refreshed. Refresh before making another change.",
        );
      }
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  }
  const [message, setMessage] = useState(
    () => messages[new URLSearchParams(window.location.search).get("message") ?? ""] ?? "",
  );
  const vaults = data.vaults ?? [];
  const deleted = data.deletedVaults ?? [];
  return (
    <MutationContext.Provider value={{ busy: busy || Boolean(refreshError), perform }}>
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <div className="app-bar">
        <a className="brand" href="/vault-dashboard" aria-label="NoX Backend dashboard">
          <span className="brand-mark" aria-hidden="true">
            <span>N</span>
          </span>
          <strong>NoX Backend</strong>
        </a>
        <span className="app-bar-label">
          {data.authenticated ? (
            <>
              <span className="status-dot" aria-hidden="true" />
              Personal workspace
            </>
          ) : (
            "Self-hosted vault storage"
          )}
        </span>
      </div>
      <main id="main-content">
        {data.authenticated ? (
          <>
            <header>
              <div>
                <p className="eyebrow">YOUR WORKSPACE</p>
                <h1>
                  {data.user.firstName ? `Welcome back, ${data.user.firstName}` : "Your workspace"}
                </h1>
                <div className="account-line">
                  <span className="muted">{data.user.email}</span>
                  <span className="badge">
                    {data.user.isAdminManager ? "Admin Manager" : data.isAdmin ? "Admin" : "Member"}
                  </span>
                </div>
              </div>
              <div className="header-actions">
                <a
                  className="icon-button secondary"
                  href="/vault-dashboard"
                  title="Refresh dashboard"
                  aria-label="Refresh dashboard"
                >
                  <Icon name="refresh" />
                </a>
                <MutationForm path="/api/session/logout" message="Signed out.">
                  <button
                    className="icon-button secondary"
                    type="submit"
                    title="Log out"
                    aria-label="Log out"
                  >
                    <Icon name="logout" />
                  </button>
                </MutationForm>
                <MutationForm
                  path="/api/session/logout-all"
                  message="Signed out of all NoX apps."
                  confirm={() =>
                    window.confirm("Sign out of all NoX apps using this Auth session?")
                  }
                >
                  <button className="secondary" type="submit">
                    Sign out of all NoX apps
                  </button>
                </MutationForm>
              </div>
            </header>
            {refreshError && (
              <p role="alert" className="error-message">
                {refreshError}
              </p>
            )}
            {busy && <p role="status">Saving changes…</p>}
            {message && (
              <div className="message" role="status">
                <span>{message}</span>
                <Action label="Close message" icon="x" onClick={() => setMessage("")} />
              </div>
            )}
            <div className="overview" aria-label="Storage overview">
              <div>
                <span className="overview-label">Active vaults</span>
                <strong>{vaults.length}</strong>
              </div>
              <div>
                <span className="overview-label">Stored in active vaults</span>
                <strong>
                  {formatBytes(
                    vaults.reduce((total, vault) => total + BigInt(vault.sizeBytes), 0n),
                  )}
                </strong>
              </div>
              <div>
                <span className="overview-label">Deleted vaults</span>
                <strong>{deleted.length}</strong>
              </div>
            </div>
            <Credentials data={data} />
            <div className="stack">
              <Vaults vaults={vaults} deleted={deleted} />
              {data.isAdmin && <Users users={data.users ?? []} />}
              {data.isAdmin && data.integrationError && (
                <p role="alert" className="error-message">
                  {data.integrationError}
                </p>
              )}
              {data.isAdmin && <Connections connections={data.connections ?? []} />}
              {data.isAdmin && <SuiteAdministration key={data.user.id} actorId={data.user.id} />}
            </div>
          </>
        ) : (
          <div className="login-panel">
            <div className="login-icon" aria-hidden="true">
              <Icon name="vault" />
            </div>
            <p className="eyebrow">NOX BACKEND</p>
            <h1>
              Your vaults.
              <br />
              Your infrastructure.
            </h1>
            <p className="muted">
              Manage your vaults, connect your apps and keep your data on your own server. Sign in
              through NoX Login with an authorized Google account to get started.
            </p>
            {data.oauthConfigured ? (
              <a className="button" href={data.loginUrl}>
                NoX Login
              </a>
            ) : (
              <p className="muted">
                NoX Login is not configured. Apply the Backend Auth setup with the running NoX Auth
                service.
              </p>
            )}
          </div>
        )}
      </main>
      <footer>
        NoX Backend <span aria-hidden="true">·</span> Your data, on your server.
      </footer>
    </MutationContext.Provider>
  );
}
export function App() {
  const [data, setData] = useState<Dashboard | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    requestJSON<Dashboard>("/api/dashboard", { signal: controller.signal })
      .then((value) => {
        if (value.authenticated && value.renewLogin) {
          window.location.assign(value.loginUrl);
          return;
        }
        setData(value);
        setFailed(false);
      })
      .catch((error) => {
        if (error.name !== "AbortError") setFailed(true);
      });
    return () => controller.abort();
  }, [attempt]);
  useEffect(() => {
    if (!data?.authenticated) return;
    const timer = window.setInterval(
      () => {
        if (document.visibilityState !== "visible") return;
        requestJSON<Dashboard>("/api/dashboard")
          .then((value) => {
            if (value.authenticated && value.renewLogin) {
              window.location.assign(value.loginUrl);
              return;
            }
            setData(value);
          })
          .catch(() => {});
      },
      60 * 60 * 1000,
    );
    return () => window.clearInterval(timer);
  }, [data?.authenticated]);
  if (data)
    return (
      <DashboardView
        data={data}
        reload={async () => {
          const next = await requestJSON<Dashboard>("/api/dashboard");
          if (next.authenticated && next.renewLogin) {
            window.location.assign(next.loginUrl);
            return;
          }
          setData(next);
        }}
      />
    );
  return (
    <main>
      <h1>NoX Backend</h1>
      {failed ? (
        <>
          <p role="alert">
            Unable to load the dashboard. Check the Backend connection and try again.
          </p>
          <button
            type="button"
            onClick={() => {
              setFailed(false);
              setAttempt(attempt + 1);
            }}
          >
            Try again
          </button>
        </>
      ) : (
        <p role="status">Loading your workspace…</p>
      )}
    </main>
  );
}
