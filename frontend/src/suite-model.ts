export interface SuiteCredential {
  credentialId: string;
  revision: string;
  state: string;
  current: boolean;
  expiresAt?: string;
}
export interface SuiteService {
  serviceId: string;
  productId: string;
  associationId: string;
  baseUrl: string;
  purpose: string;
  state: string;
  revision: string;
  headRevision: string;
  current: boolean;
  credentials: SuiteCredential[];
  deployments: { deploymentId: string; productId: string; baseUrl: string; revision: string }[];
}
export interface SuitePairing {
  pairingId: string;
  productId: string;
  baseUrl: string;
  purpose: string;
  state: string;
  revision: string;
  expiresAt: string;
  serviceId?: string;
}
export interface SuiteProduct {
  productId: string;
  associationId: string;
  revision: string;
  declaration: {
    displayName: string;
    productVersion: string;
    capabilities: { suiteMcp?: { path: string } };
  };
  connection: { baseUrl: string };
  managedBy?: { productId: string; deploymentId: string };
}
export interface SuiteAdministration {
  configuration: {
    backendOrigin: string;
    canManage: boolean;
    canEnroll: boolean;
    enrollmentReason?: string;
    reason: string;
    directMcpEnabled: boolean;
    suiteMcpEnabled: boolean;
    allowedCidrs: string[] | null;
    allowedPorts: string[] | null;
  };
  state: {
    issuer: string;
    keyRevision: string;
    registryRevision: string;
    observedAt: string;
    signingKeys: { keyId: string; state: string; retiresAt?: string }[];
    services: SuiteService[];
    pairings: SuitePairing[];
    products: SuiteProduct[];
    blockedProducts: string[];
    observations: {
      observation: { productId: string; source: string; state: string };
      freshUntil: string;
    }[];
  };
}

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
function strings(value: unknown, fields: string[]): value is Record<string, unknown> {
  return object(value) && fields.every((field) => typeof value[field] === "string");
}
function arrayOf(value: unknown, valid: (item: unknown) => boolean): boolean {
  return Array.isArray(value) && value.every(valid);
}
export function parseSuiteAdministration(value: unknown): SuiteAdministration {
  if (!object(value) || !object(value.configuration) || !object(value.state))
    throw Error("Invalid Suite administration response.");
  const configuration = value.configuration,
    state = value.state;
  const valid =
    typeof configuration.canManage === "boolean" &&
    typeof configuration.canEnroll === "boolean" &&
    typeof configuration.directMcpEnabled === "boolean" &&
    typeof configuration.suiteMcpEnabled === "boolean" &&
    strings(configuration, ["backendOrigin", "reason"]) &&
    strings(state, ["issuer", "keyRevision", "registryRevision", "observedAt"]) &&
    arrayOf(state.blockedProducts, (item) => typeof item === "string") &&
    arrayOf(state.signingKeys, (item) => strings(item, ["keyId", "state"])) &&
    arrayOf(state.pairings, (item) =>
      strings(item, [
        "pairingId",
        "productId",
        "baseUrl",
        "purpose",
        "state",
        "revision",
        "expiresAt",
      ]),
    ) &&
    arrayOf(
      state.services,
      (item) =>
        strings(item, [
          "serviceId",
          "productId",
          "associationId",
          "baseUrl",
          "purpose",
          "state",
          "revision",
          "headRevision",
        ]) &&
        typeof item.current === "boolean" &&
        arrayOf(
          item.credentials,
          (credential) =>
            strings(credential, ["credentialId", "revision", "state"]) &&
            typeof credential.current === "boolean",
        ) &&
        arrayOf(item.deployments, (deployment) =>
          strings(deployment, ["deploymentId", "productId", "baseUrl", "revision"]),
        ),
    ) &&
    arrayOf(
      state.products,
      (item) =>
        strings(item, ["productId", "associationId", "revision"]) &&
        object(item.declaration) &&
        strings(item.declaration, ["displayName", "productVersion"]) &&
        object(item.declaration.capabilities) &&
        strings(item.connection, ["baseUrl"]),
    ) &&
    arrayOf(
      state.observations,
      (item) =>
        strings(item, ["freshUntil"]) &&
        strings(item.observation, ["productId", "source", "state"]),
    );
  if (!valid)
    throw Error("Invalid Suite administration response. Refresh before changing integration.");
  return value as unknown as SuiteAdministration;
}
