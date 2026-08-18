import { describe, expect, it } from "vitest";
import manifest from "./assets/manifest.json";
import { validateAssetManifest } from "./asset-manifest";

describe("world asset manifest", () => {
  const firstAsset = manifest.assets[0]!;

  it("validates the current repository assets", () => {
    expect(validateAssetManifest(manifest).assets).toHaveLength(2);
  });

  it.each([
    [{ ...manifest, assets: [{ ...firstAsset, type: "image" }] }, "Unknown type"],
    [{ ...manifest, assets: [{ ...firstAsset, path: "../../outside.png" }] }, "path"],
    [{ ...manifest, assets: [firstAsset, { ...firstAsset, key: "duplicate" }] }, "Duplicate"],
    [{ ...manifest, assets: [{ ...firstAsset, provenance: { ...firstAsset.provenance, kind: "unknown" } }] }, "Unknown provenance.kind"],
    [{ ...manifest, assets: [{ ...firstAsset, provenance: { ...firstAsset.provenance, license: "unknown" } }] }, "Unknown provenance.license"],
  ])("rejects malformed metadata", (input, message) => {
    expect(() => validateAssetManifest(input)).toThrow(message);
  });
});
