import { describe, expect, it } from "vitest";
// @ts-expect-error Vite provides the raw asset importer at test runtime.
import mapText from "./maps/mission-world-v1.tmj?raw";
import { createWorldMapDebugOverlay, loadWorldMap, validateWorldMap } from "./map-loader";
import { WORLD_ZONES } from "./world-model";

const mapJson = JSON.parse(mapText) as TiledFixture;

describe("mission-world-v1 map loader", () => {
  it("loads all zones and produces deterministic debug overlay data", () => {
    const first = loadWorldMap(mapJson);
    const second = loadWorldMap(JSON.parse(JSON.stringify(mapJson)));
    expect(first).toEqual(second);
    expect(first.zones.map((zone) => zone.id)).toEqual([...WORLD_ZONES]);
    expect(first.zones.every((zone) => zone.anchors.length >= 4)).toBe(true);
    expect(createWorldMapDebugOverlay(first)).toEqual(createWorldMapDebugOverlay(second));
  });

  it("fails closed for duplicate, missing, illegal, and under-specified geometry", () => {
    expect(() => validateWorldMap({ ...mapJson, layers: [...mapJson.layers, mapJson.layers[0]!] })).toThrow(/Duplicate map layer/);
    const missingZone = structuredClone(mapJson);
    missingZone.layers[0]!.objects = missingZone.layers[0]!.objects.slice(1);
    expect(() => validateWorldMap(missingZone)).toThrow(/six WORLD_ZONES/);
    const invalidAnchor = structuredClone(mapJson);
    invalidAnchor.layers[1]!.objects[0]!.x = -1;
    expect(() => validateWorldMap(invalidAnchor)).toThrow(/outside its zone/);
    const tooFew = structuredClone(mapJson);
    tooFew.layers[1]!.objects = tooFew.layers[1]!.objects.slice(1);
    expect(() => validateWorldMap(tooFew)).toThrow(/at least four/);
  });
});

interface TiledFixture {
  layers: TiledLayer[];
  [key: string]: unknown;
}

interface TiledLayer {
  objects: TiledObject[];
  [key: string]: unknown;
}

interface TiledObject {
  [key: string]: unknown;
}
