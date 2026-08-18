import { WORLD_ZONES, type WorldZone } from "./world-model";

export type MapPoint = Readonly<{ x: number; y: number }>;
export type MapBounds = Readonly<{ x: number; y: number; width: number; height: number }>;
export type MapAnchorKind = "spawn" | "work";

export interface WorldMapZone {
  readonly id: WorldZone;
  readonly bounds: MapBounds;
  readonly labelAnchor: MapPoint;
  readonly anchors: readonly WorldMapAnchor[];
}

export interface WorldMapAnchor {
  readonly id: string;
  readonly zoneId: WorldZone;
  readonly kind: MapAnchorKind;
  readonly order: number;
  readonly point: MapPoint;
}

export interface WorldMapDefinition {
  readonly width: number;
  readonly height: number;
  readonly tileWidth: number;
  readonly tileHeight: number;
  readonly pixelWidth: number;
  readonly pixelHeight: number;
  readonly zones: readonly WorldMapZone[];
}

export interface WorldMapDebugOverlay {
  readonly mapSize: MapBounds;
  readonly zones: readonly {
    readonly id: WorldZone;
    readonly bounds: MapBounds;
    readonly labelAnchor: MapPoint;
    readonly anchors: readonly {
      readonly id: string;
      readonly kind: MapAnchorKind;
      readonly point: MapPoint;
    }[];
  }[];
}

const ZONE_LAYER = "zones";
const ANCHOR_LAYER = "anchors";

/** Parse and validate a Tiled JSON map without importing a renderer. */
export function loadWorldMap(input: unknown): WorldMapDefinition {
  const raw = typeof input === "string" ? parseJson(input) : input;
  return validateWorldMap(raw);
}

export function validateWorldMap(input: unknown): WorldMapDefinition {
  const map = asRecord(input, "map");
  const width = positiveInteger(map.width, "map.width");
  const height = positiveInteger(map.height, "map.height");
  const tileWidth = positiveInteger(map.tilewidth, "map.tilewidth");
  const tileHeight = positiveInteger(map.tileheight, "map.tileheight");
  const layers = asArray(map.layers, "map.layers");
  const layerByName = new Map<string, Record<string, unknown>>();
  for (const layerValue of layers) {
    const layer = asRecord(layerValue, "map layer");
    const name = nonEmptyString(layer.name, "map layer.name");
    if (layerByName.has(name)) throw new Error(`Duplicate map layer: ${name}`);
    layerByName.set(name, layer);
  }
  const zoneLayer = objectLayer(layerByName.get(ZONE_LAYER), ZONE_LAYER);
  const anchorLayer = objectLayer(layerByName.get(ANCHOR_LAYER), ANCHOR_LAYER);
  const pixelWidth = width * tileWidth;
  const pixelHeight = height * tileHeight;
  const zonesById = new Map<WorldZone, WorldMapZone>();

  for (const objectValue of zoneLayer.objects) {
    const object = asRecord(objectValue, "zone object");
    const id = zoneId(property(object, "zoneId"), "zone object zoneId");
    if (zonesById.has(id)) throw new Error(`Duplicate zone: ${id}`);
    const bounds = rectangle(object, `zone ${id}`);
    assertInside(bounds, pixelWidth, pixelHeight, `zone ${id}`);
    const labelAnchor = pointFromZone(object, `zone ${id} labelAnchor`);
    assertInsidePoint(labelAnchor, bounds, `zone ${id} labelAnchor`);
    zonesById.set(id, { id, bounds, labelAnchor, anchors: [] });
  }
  if (zonesById.size !== WORLD_ZONES.length || WORLD_ZONES.some((id) => !zonesById.has(id))) {
    throw new Error("Map must contain each of the six WORLD_ZONES exactly once");
  }

  const anchorsByZone = new Map<WorldZone, WorldMapAnchor[]>();
  const anchorIds = new Set<string>();
  for (const objectValue of anchorLayer.objects) {
    const object = asRecord(objectValue, "anchor object");
    const id = nonEmptyString(object.name, "anchor.name");
    if (anchorIds.has(id)) throw new Error(`Duplicate anchor: ${id}`);
    anchorIds.add(id);
    const anchorZoneId = zoneId(property(object, "zoneId"), `anchor ${id} zoneId`);
    const kind = property(object, "anchorKind");
    if (kind !== "spawn" && kind !== "work") throw new Error(`Invalid anchor kind for ${id}`);
    const order = finiteNumber(property(object, "order"), `anchor ${id}.order`);
    if (!Number.isInteger(order) || order < 0) throw new Error(`Invalid anchor order for ${id}`);
    const point = pointFromObject(object, `anchor ${id}`);
    const zone = zonesById.get(anchorZoneId)!;
    assertInsidePoint(point, zone.bounds, `anchor ${id}`);
    const anchors = anchorsByZone.get(anchorZoneId) ?? [];
    if (anchors.some((anchor) => anchor.order === order)) throw new Error(`Duplicate anchor order in ${anchorZoneId}: ${order}`);
    anchors.push({ id, zoneId: anchorZoneId, kind, order, point });
    anchorsByZone.set(anchorZoneId, anchors);
  }

  const zones = WORLD_ZONES.map((id) => {
    const anchors = [...(anchorsByZone.get(id) ?? [])].sort((left, right) => left.order - right.order || compareText(left.id, right.id));
    if (anchors.length < 4 || !anchors.some((anchor) => anchor.kind === "spawn") || !anchors.some((anchor) => anchor.kind === "work")) {
      throw new Error(`Zone ${id} must contain at least four spawn/work anchors including both kinds`);
    }
    return { ...zonesById.get(id)!, anchors };
  });
  return { width, height, tileWidth, tileHeight, pixelWidth, pixelHeight, zones };
}

export function createWorldMapDebugOverlay(map: WorldMapDefinition): WorldMapDebugOverlay {
  return {
    mapSize: { x: 0, y: 0, width: map.pixelWidth, height: map.pixelHeight },
    zones: map.zones.map((zone) => ({
      id: zone.id,
      bounds: { ...zone.bounds },
      labelAnchor: { ...zone.labelAnchor },
      anchors: zone.anchors.map(({ id, kind, point }) => ({ id, kind, point: { ...point } })),
    })),
  };
}

function parseJson(input: string): unknown {
  try { return JSON.parse(input) as unknown; } catch { throw new Error("Invalid Tiled JSON map"); }
}
function objectLayer(layer: Record<string, unknown> | undefined, name: string): { objects: unknown[] } {
  if (!layer || layer.type !== "objectgroup") throw new Error(`Missing Tiled object layer: ${name}`);
  return { objects: asArray(layer.objects, `${name}.objects`) };
}
function property(object: Record<string, unknown>, name: string): unknown {
  const properties = asArray(object.properties, `${object.name ?? "object"}.properties`);
  const found = properties.find((value) => asRecord(value, "property").name === name);
  return found === undefined ? undefined : asRecord(found, "property").value;
}
function rectangle(object: Record<string, unknown>, label: string): MapBounds {
  const x = finiteNumber(object.x, `${label}.x`);
  const y = finiteNumber(object.y, `${label}.y`);
  const width = positiveNumber(object.width, `${label}.width`);
  const height = positiveNumber(object.height, `${label}.height`);
  return { x, y, width, height };
}
function pointFromObject(object: Record<string, unknown>, label: string): MapPoint {
  return { x: finiteNumber(object.x, `${label}.x`), y: finiteNumber(object.y, `${label}.y`) };
}
function pointFromZone(object: Record<string, unknown>, label: string): MapPoint {
  const labelX = property(object, "labelX");
  const labelY = property(object, "labelY");
  if (labelX !== undefined || labelY !== undefined) return { x: finiteNumber(labelX, `${label}.x`), y: finiteNumber(labelY, `${label}.y`) };
  return pointFromObject(object, label);
}
function zoneId(value: unknown, label: string): WorldZone {
  if (!WORLD_ZONES.includes(value as WorldZone)) throw new Error(`Invalid ${label}`);
  return value as WorldZone;
}
function assertInside(bounds: MapBounds, mapWidth: number, mapHeight: number, label: string): void {
  if (bounds.x < 0 || bounds.y < 0 || bounds.x + bounds.width > mapWidth || bounds.y + bounds.height > mapHeight) throw new Error(`${label} is outside map bounds`);
}
function assertInsidePoint(point: MapPoint, bounds: MapBounds, label: string): void {
  if (point.x < bounds.x || point.y < bounds.y || point.x > bounds.x + bounds.width || point.y > bounds.y + bounds.height) throw new Error(`${label} is outside its zone`);
}
function asRecord(value: unknown, label: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error(`Invalid ${label}`);
  return value as Record<string, unknown>;
}
function asArray(value: unknown, label: string): unknown[] { if (!Array.isArray(value)) throw new Error(`Invalid ${label}`); return value; }
function nonEmptyString(value: unknown, label: string): string { if (typeof value !== "string" || value.length === 0) throw new Error(`Invalid ${label}`); return value; }
function finiteNumber(value: unknown, label: string): number { if (typeof value !== "number" || !Number.isFinite(value)) throw new Error(`Invalid ${label}`); return value; }
function positiveNumber(value: unknown, label: string): number { const number = finiteNumber(value, label); if (number <= 0) throw new Error(`Invalid ${label}`); return number; }
function positiveInteger(value: unknown, label: string): number { const number = positiveNumber(value, label); if (!Number.isInteger(number)) throw new Error(`Invalid ${label}`); return number; }
function compareText(left: string, right: string): number { return left < right ? -1 : left > right ? 1 : 0; }
