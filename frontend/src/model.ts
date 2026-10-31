export interface User {
  id: string;
  email: string;
  firstName: string;
  displayName: string;
  role: string;
  status: string;
  lastLoginAt: string;
  isAdminManager: boolean;
  permissions?: { canDelete: boolean; canChangeRole: boolean; canChangeStatus: boolean };
}
export interface Vault {
  vaultId: string;
  name: string;
  revision: string;
  sizeBytes: string;
  updatedAt: string;
  deletedAt?: string;
  status: string;
}
export interface FoundationConnection {
  service: "yard" | "gateway";
  origin: string;
  revision: string;
  updatedAt: string;
  status:
    | "not_configured"
    | "not_paired"
    | "pairing_pending"
    | "paired"
    | "registered"
    | "recovery_required";
}
export interface Dashboard {
  authenticated: boolean;
  oauthConfigured: boolean;
  loginUrl: string;
  serverUrl: string;
  user: User;
  apiKey: string;
  vaults: Vault[] | null;
  deletedVaults: Vault[] | null;
  isAdmin: boolean;
  users: User[] | null;
  connections?: FoundationConnection[];
}
export function formatBytes(value: string | bigint) {
  const mb = Number(value) / 1048576;
  return mb <= 0
    ? "0 MB"
    : mb < 0.1
      ? "< 0.1 MB"
      : mb >= 10
        ? `${Math.round(mb)} MB`
        : `${mb.toFixed(1)} MB`;
}
export function formatTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat("en-GB", {
        day: "2-digit",
        month: "short",
        year: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
        timeZone: "UTC",
      }).format(date) + " UTC";
}
export const messages: Record<string, string> = {
  "api-key-rotated": "A new API key was generated. Update the key in every connected app.",
  "user-saved": "User settings were saved.",
  "user-deleted": "User deleted.",
  "vault-deleted": "Vault deleted.",
  "vault-restored": "Vault restored.",
  "vault-purged": "Vault permanently deleted.",
};
