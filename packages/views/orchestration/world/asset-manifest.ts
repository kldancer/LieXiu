export const ASSET_MANIFEST_TYPES = ["tiled-map", "sprite-atlas"] as const;
export const ASSET_MANIFEST_STATUSES = ["current"] as const;
export const ASSET_PROVENANCE_KINDS = ["project-internal-original"] as const;
export const ASSET_LICENSES = ["repository-multica-license"] as const;

export type AssetManifestType = (typeof ASSET_MANIFEST_TYPES)[number];
export type AssetManifestStatus = (typeof ASSET_MANIFEST_STATUSES)[number];
export type AssetProvenanceKind = (typeof ASSET_PROVENANCE_KINDS)[number];
export type AssetLicense = (typeof ASSET_LICENSES)[number];

export interface AssetManifestEntry {
  readonly key: string;
  readonly replacementKey: string;
  readonly type: AssetManifestType;
  readonly status: AssetManifestStatus;
  readonly version: string;
  readonly path: string;
  readonly provenance: { readonly kind: AssetProvenanceKind; readonly license: AssetLicense; readonly evidence: string };
}

export interface AssetManifest {
  readonly schemaVersion: 1;
  readonly assets: readonly AssetManifestEntry[];
}

const MANIFEST_KEYS = new Set(["schemaVersion", "assets"]);
const ENTRY_KEYS = new Set(["key", "replacementKey", "type", "status", "version", "path", "provenance"]);
const PROVENANCE_KEYS = new Set(["kind", "license", "evidence"]);
const REPO_PATH = /^(?!\/)(?!.*(?:^|\/)\.\.(?:\/|$))(?!.*(?:^|\/)(?:\.|\.\.)(?:\/|$))[A-Za-z0-9._/-]+$/;

export function validateAssetManifest(input: unknown): AssetManifest {
  const manifest = record(input, "manifest");
  exactKeys(manifest, MANIFEST_KEYS, "manifest");
  if (manifest.schemaVersion !== 1) throw new Error("Unsupported asset manifest schemaVersion");
  if (!Array.isArray(manifest.assets)) throw new Error("manifest.assets must be an array");
  const keys = new Set<string>();
  const replacementKeys = new Set<string>();
  const paths = new Set<string>();
  const assets = manifest.assets.map((value, index) => {
    const entry = record(value, `manifest.assets[${index}]`);
    exactKeys(entry, ENTRY_KEYS, `manifest.assets[${index}]`);
    const result = {
      key: string(entry.key, "key"), replacementKey: string(entry.replacementKey, "replacementKey"),
      type: oneOf(entry.type, ASSET_MANIFEST_TYPES, "type"), status: oneOf(entry.status, ASSET_MANIFEST_STATUSES, "status"),
      version: string(entry.version, "version"), path: string(entry.path, "path"),
      provenance: validateProvenance(entry.provenance),
    } as AssetManifestEntry;
    if (keys.has(result.key) || replacementKeys.has(result.replacementKey) || paths.has(result.path)) throw new Error(`Duplicate asset identity: ${result.key}`);
    if (!REPO_PATH.test(result.path) || result.path.startsWith("packages/views/orchestration/world/assets/")) throw new Error(`Asset path must stay within the repository: ${result.path}`);
    keys.add(result.key); replacementKeys.add(result.replacementKey); paths.add(result.path);
    return result;
  });
  return { schemaVersion: 1, assets };
}

function validateProvenance(input: unknown): AssetManifestEntry["provenance"] {
  const value = record(input, "provenance"); exactKeys(value, PROVENANCE_KEYS, "provenance");
  return {
    kind: oneOf(value.kind, ASSET_PROVENANCE_KINDS, "provenance.kind"),
    license: oneOf(value.license, ASSET_LICENSES, "provenance.license"),
    evidence: string(value.evidence, "provenance.evidence"),
  };
}
function record(input: unknown, label: string): Record<string, unknown> { if (typeof input !== "object" || input === null || Array.isArray(input)) throw new Error(`${label} must be an object`); return input as Record<string, unknown>; }
function exactKeys(value: Record<string, unknown>, allowed: Set<string>, label: string): void { for (const key of Object.keys(value)) if (!allowed.has(key)) throw new Error(`Unknown ${label} field: ${key}`); }
function string(input: unknown, label: string): string { if (typeof input !== "string" || input.length === 0) throw new Error(`${label} must be a non-empty string`); return input; }
function oneOf<T extends readonly string[]>(input: unknown, allowed: T, label: string): T[number] { if (typeof input !== "string" || !allowed.includes(input)) throw new Error(`Unknown ${label}: ${String(input)}`); return input as T[number]; }
