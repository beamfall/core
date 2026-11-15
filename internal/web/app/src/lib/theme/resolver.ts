/*
 * Beamfall theme resolver, TypeScript port (ADR-0231 D1-D5; CORE-1179).
 *
 * Turns an ordered list of theme documents plus the OS accessibility flags into a dark and a
 * light palette of the 18 colour roles, the resolved knobs and the list of contrast repairs, or
 * into one rejection. The behaviour is defined by the design-system theme resolver
 * specification and pinned by its golden vectors; this module is an independent implementation
 * of that behaviour and carries neither the specification text nor the vectors.
 *
 * The module is deliberately self-contained (no imports, erasable TypeScript only) so that:
 *   - the token generator can import it from plain Node to precompute preset palettes, and
 *   - other web clients can vendor it byte-for-byte (beamfall-web does, under a drift check).
 *
 * Base palettes are injected, never read from disk: call compileBasePalettes() with the token
 * release's `theme.cinema` / `theme.workbench` maps, or use a precomputed set.
 */

export const THEME_SCHEMA_VERSION = 3;

export const THEME_ROLES = [
  "bg-root",
  "bg-surface",
  "bg-raised",
  "bg-overlay",
  "line",
  "line-soft",
  "border-strong",
  "text",
  "text-muted",
  "accent",
  "accent-ink",
  "accent-on-light",
  "focus-ring",
  "state-direct",
  "state-remote",
  "state-restricted",
  "state-warning",
  "state-danger",
] as const;

export type ThemeRole = (typeof THEME_ROLES)[number];
export type ThemeScheme = "dark" | "light";
export const THEME_SCHEMES: readonly ThemeScheme[] = ["dark", "light"];

/** 8-bit sRGB channels plus 8-bit alpha. */
export interface Rgba {
  r: number;
  g: number;
  b: number;
  a: number;
}

export type Palette = Record<ThemeRole, Rgba>;
export type HexPalette = Record<ThemeRole, string>;

export interface BasePalettes {
  dark: Palette;
  light: Palette;
}

export interface ThemeKnobs {
  shape: string;
  density: string;
  typeScale: string;
  typeface: string;
  motion: string;
  material: string;
  artworkTint: string;
}

export interface ThemeScales {
  radius: number;
  spacing: number;
  type: number;
}

export interface ThemeRepair {
  scheme: ThemeScheme;
  role: ThemeRole;
  from: string;
  to: string;
  reason: "surface-opaque" | "surface-range" | "fill-opaque" | "contrast";
  floor?: number;
  against?: string[];
}

export interface ResolvedTheme {
  schemaVersion: number;
  dark: HexPalette;
  light: HexPalette;
  knobs: ThemeKnobs;
  scales: ThemeScales;
  repairs: ThemeRepair[];
}

export interface ThemeRejection {
  error: { code: string; path: string; layer: number };
}

export interface ResolveOptions {
  highContrast?: boolean;
  reduceTransparency?: boolean;
  reduceMotion?: boolean;
  /** Per-device knob adjustments; only `density` and `type_scale` are accepted. */
  device?: unknown;
}

export function isThemeRejection(value: ResolvedTheme | ThemeRejection): value is ThemeRejection {
  return "error" in value;
}

// ---------------------------------------------------------------------------------------------
// Vocabularies

type KnobName =
  | "shape"
  | "density"
  | "type_scale"
  | "typeface"
  | "motion"
  | "material"
  | "artwork_tint";

/** Knob vocabularies in specification order; the first value of each is its default. */
const KNOB_VALUES: ReadonlyArray<readonly [KnobName, readonly string[]]> = [
  ["shape", ["standard", "sharp", "round"]],
  ["density", ["standard", "compact", "comfortable"]],
  ["type_scale", ["standard", "small", "large", "larger"]],
  ["typeface", ["house", "system"]],
  ["motion", ["full", "reduced", "none"]],
  ["material", ["glass", "solid"]],
  ["artwork_tint", ["on", "off"]],
];
const KNOB_NAMES: readonly string[] = KNOB_VALUES.map(([name]) => name);
const DEVICE_KNOBS: readonly KnobName[] = ["density", "type_scale"];

const RADIUS_SCALE: Record<string, number> = { standard: 1, sharp: 0.5, round: 1.5 };
const SPACING_SCALE: Record<string, number> = { standard: 1, compact: 0.875, comfortable: 1.125 };
const TYPE_SCALE: Record<string, number> = { standard: 1, small: 0.9, large: 1.15, larger: 1.3 };

const MOODS: readonly string[] = ["muted", "balanced", "vivid"];
const MOOD_CHROMA: Record<string, { neutral: number; accent: number }> = {
  muted: { neutral: 0.5, accent: 0.7 },
  balanced: { neutral: 1, accent: 1 },
  vivid: { neutral: 1.6, accent: 1.2 },
};

/** v2 Skin base themes and the scheme each upgrades into. */
const V2_SCHEME_OF: ReadonlyArray<readonly [string, ThemeScheme]> = [
  ["cinema", "dark"],
  ["workbench-dark", "dark"],
  ["workbench", "light"],
];
/** The roles a v2 Skin may override, in the order the v2 payload lists them. */
const V2_ROLES: readonly ThemeRole[] = [
  "accent",
  "accent-ink",
  "accent-on-light",
  "bg-raised",
  "bg-root",
  "bg-surface",
  "border-strong",
  "line",
  "state-danger",
  "state-direct",
  "state-remote",
  "state-restricted",
  "state-warning",
  "text",
  "text-muted",
];

const SURFACE_ROLES: readonly ThemeRole[] = ["bg-root", "bg-surface", "bg-raised"];
const ACCENT_FAMILY: readonly ThemeRole[] = [
  "accent",
  "accent-ink",
  "accent-on-light",
  "focus-ring",
];
/** Neutral roles and the share of the neutral seed's chroma each one takes. */
const NEUTRAL_SHARE: ReadonlyArray<readonly [ThemeRole, number]> = [
  ["bg-root", 0.35],
  ["bg-surface", 0.35],
  ["bg-raised", 0.35],
  ["bg-overlay", 0.35],
  ["line", 0.45],
  ["border-strong", 0.45],
  ["text", 0.12],
  ["text-muted", 0.35],
];
const NEUTRAL_FAMILY: readonly ThemeRole[] = NEUTRAL_SHARE.map(([role]) => role);
const NEUTRAL_CHROMA_LIMIT = 0.06;
const SURFACE_LIGHTNESS: Record<ThemeScheme, readonly [number, number]> = {
  dark: [0, 0.32],
  light: [0.9, 1],
};
const INK_DARK: Rgba = { r: 7, g: 12, b: 16, a: 255 };
const INK_LIGHT: Rgba = { r: 255, g: 255, b: 255, a: 255 };

// ---------------------------------------------------------------------------------------------
// Colour arithmetic. Constants and the order of operations are fixed by the specification so
// that every port lands on the same 8-bit values.

const HEX_PATTERN = /^#([0-9a-fA-F]{6})([0-9a-fA-F]{2})?$/;
const SEED_PATTERN = /^#[0-9a-fA-F]{6}$/;

/** Parse `#rrggbb` / `#rrggbbaa` (any case); null when malformed. */
export function parseThemeHex(value: unknown): Rgba | null {
  if (typeof value !== "string") return null;
  const match = HEX_PATTERN.exec(value);
  if (!match) return null;
  const rgb = Number.parseInt(match[1] ?? "", 16);
  const alpha = match[2];
  return {
    r: Math.floor(rgb / 65536) % 256,
    g: Math.floor(rgb / 256) % 256,
    b: rgb % 256,
    a: alpha === undefined ? 255 : Number.parseInt(alpha, 16),
  };
}

function byte(n: number): string {
  return n.toString(16).padStart(2, "0");
}

/** Canonical lowercase hex: `#rrggbb` when opaque, `#rrggbbaa` otherwise. */
export function formatThemeHex(color: Rgba): string {
  const body = `#${byte(color.r)}${byte(color.g)}${byte(color.b)}`;
  return color.a === 255 ? body : body + byte(color.a);
}

const RGBA_CSS = /^rgba\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*([0-9]*\.?[0-9]+)\s*\)$/;

/** Token-source colour: hex, or `rgba(r, g, b, a)` with alpha rounded to 8 bits. */
function parseTokenColor(value: string): Rgba | null {
  const hex = parseThemeHex(value);
  if (hex) return hex;
  const m = RGBA_CSS.exec(value);
  if (!m) return null;
  return {
    r: Number(m[1]),
    g: Number(m[2]),
    b: Number(m[3]),
    a: Math.round(Number(m[4]) * 255),
  };
}

function decode(channel: number): number {
  const c = channel / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}

function encode(linear: number): number {
  return linear <= 0.0031308 ? linear * 12.92 : 1.055 * linear ** (1 / 2.4) - 0.055;
}

function clampUnit(x: number): number {
  return Math.min(1, Math.max(0, x));
}

function toByte(linear: number): number {
  return Math.round(clampUnit(encode(clampUnit(linear))) * 255);
}

interface Lch {
  l: number;
  c: number;
  h: number;
}

function oklch(color: Rgba): Lch {
  const r = decode(color.r);
  const g = decode(color.g);
  const b = decode(color.b);
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b);
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b);
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b);
  const lightness = 0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s;
  const aAxis = 1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s;
  const bAxis = 0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s;
  let hue = (Math.atan2(bAxis, aAxis) * 180) / Math.PI;
  if (hue < 0) hue += 360;
  return { l: lightness, c: Math.sqrt(aAxis * aAxis + bAxis * bAxis), h: hue };
}

function linearRgb(lightness: number, chroma: number, hue: number): [number, number, number] {
  const angle = (hue * Math.PI) / 180;
  const a = chroma * Math.cos(angle);
  const b = chroma * Math.sin(angle);
  const l = (lightness + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m = (lightness - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s = (lightness - 0.0894841775 * a - 1.291485548 * b) ** 3;
  return [
    4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  ];
}

const GAMUT_SLACK = 1e-7;

function displayable(lightness: number, chroma: number, hue: number): boolean {
  return linearRgb(lightness, chroma, hue).every((v) => v >= -GAMUT_SLACK && v <= 1 + GAMUT_SLACK);
}

/** OKLCH to 8 bits; out-of-gamut colours keep lightness and hue and give up chroma. */
function fromLch(lch: Lch, alpha: number): Rgba {
  const lightness = clampUnit(lch.l);
  let chroma = Math.max(0, lch.c);
  if (!displayable(lightness, chroma, lch.h)) {
    let inside = 0;
    let outside = chroma;
    for (let step = 0; step < 24; step++) {
      const probe = (inside + outside) / 2;
      if (displayable(lightness, probe, lch.h)) inside = probe;
      else outside = probe;
    }
    chroma = inside;
  }
  const [r, g, b] = linearRgb(lightness, chroma, lch.h);
  return { r: toByte(r), g: toByte(g), b: toByte(b), a: alpha };
}

/** Composite over an opaque backdrop per 8-bit channel; the result is opaque. */
function flatten(fg: Rgba, bg: Rgba): Rgba {
  const k = fg.a / 255;
  return {
    r: Math.round(fg.r * k + bg.r * (1 - k)),
    g: Math.round(fg.g * k + bg.g * (1 - k)),
    b: Math.round(fg.b * k + bg.b * (1 - k)),
    a: 255,
  };
}

function luminance(color: Rgba): number {
  return 0.2126 * decode(color.r) + 0.7152 * decode(color.g) + 0.0722 * decode(color.b);
}

/** WCAG 2.2 contrast of `fg` drawn on opaque `bg` (a translucent fg is composited first). */
export function contrastOf(fg: Rgba, bg: Rgba): number {
  const top = luminance(fg.a === 255 ? fg : flatten(fg, bg));
  const bottom = luminance(bg);
  const hi = Math.max(top, bottom);
  const lo = Math.min(top, bottom);
  return (hi + 0.05) / (lo + 0.05);
}

/** Convenience: contrast between two canonical hex colours (fg composited over bg). */
export function contrastRatio(fgHex: string, bgHex: string): number {
  const fg = parseThemeHex(fgHex);
  const bg = parseThemeHex(bgHex);
  if (!fg || !bg) throw new Error(`contrastRatio: not a hex colour (${fgHex}, ${bgHex})`);
  return contrastOf(fg, bg);
}

// ---------------------------------------------------------------------------------------------
// Document validation

class Rejection {
  code: string;
  path: string;
  constructor(code: string, segments: readonly string[]) {
    this.code = code;
    this.path = segments.map((s) => `/${s.replace(/~/g, "~0").replace(/\//g, "~1")}`).join("");
  }
}

function refuse(code: string, ...segments: string[]): never {
  throw new Rejection(code, segments);
}

type JsonObject = Record<string, unknown>;

function isPlainObject(value: unknown): value is JsonObject {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function has(object: JsonObject, key: string): boolean {
  return Object.hasOwn(object, key);
}

function codePointOrder(x: string, y: string): number {
  const xs = Array.from(x);
  const ys = Array.from(y);
  const shared = Math.min(xs.length, ys.length);
  for (let i = 0; i < shared; i++) {
    const delta = (xs[i]?.codePointAt(0) ?? 0) - (ys[i]?.codePointAt(0) ?? 0);
    if (delta !== 0) return delta;
  }
  return xs.length - ys.length;
}

/** Reject the first unexpected key (in code-point order) before any value is looked at. */
function onlyKeys(
  object: JsonObject,
  allowed: readonly string[],
  code: string,
  at: string[],
): void {
  let first: string | null = null;
  for (const key of Object.keys(object)) {
    if (allowed.includes(key)) continue;
    if (first === null || codePointOrder(key, first) < 0) first = key;
  }
  if (first !== null) refuse(code, ...at, first);
}

const WHITE_SPACE_POINTS = new Set([
  0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004,
  0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000,
]);

function acceptableName(value: unknown): boolean {
  if (typeof value !== "string") return false;
  let scalars = 0;
  let visible = false;
  for (let i = 0; i < value.length; i++) {
    const unit = value.charCodeAt(i);
    let point = unit;
    if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(i + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return false;
      point = (unit - 0xd800) * 0x400 + (next - 0xdc00) + 0x10000;
      i++;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) {
      return false;
    }
    scalars++;
    if (!WHITE_SPACE_POINTS.has(point)) visible = true;
  }
  return visible && scalars <= 64;
}

type RoleColors = Partial<Record<ThemeRole, Rgba>>;

interface ThemeLayer {
  accent?: Rgba;
  neutral?: Rgba;
  mood?: string;
  overrides: Record<ThemeScheme, RoleColors>;
  knobs: Partial<Record<KnobName, string>>;
}

function emptyLayer(): ThemeLayer {
  return { overrides: { dark: {}, light: {} }, knobs: {} };
}

function readRoleColors(map: JsonObject, roles: readonly ThemeRole[], at: string[]): RoleColors {
  onlyKeys(map, roles, "unknown-role", at);
  const colors: RoleColors = {};
  for (const role of roles) {
    if (!has(map, role)) continue;
    const color = parseThemeHex(map[role]);
    if (!color) refuse("invalid-color", ...at, role);
    colors[role] = color;
  }
  return colors;
}

function readV2(doc: JsonObject): ThemeLayer {
  onlyKeys(doc, ["schema_version", "base_theme_ref", "color_overrides"], "unknown-field", []);
  const ref = doc.base_theme_ref;
  const target = V2_SCHEME_OF.find(([name]) => name === ref)?.[1];
  if (target === undefined) refuse("invalid-base-theme", "base_theme_ref");
  const layer = emptyLayer();
  if (has(doc, "color_overrides")) {
    const map = doc.color_overrides;
    if (!isPlainObject(map)) refuse("invalid-overrides", "color_overrides");
    layer.overrides[target] = readRoleColors(map, V2_ROLES, ["color_overrides"]);
  }
  return layer;
}

function readLayer(doc: unknown): ThemeLayer {
  if (!isPlainObject(doc)) refuse("not-an-object");
  const version = doc.schema_version;
  if (version === 2) return readV2(doc);
  if (version !== THEME_SCHEMA_VERSION) refuse("unsupported-schema-version", "schema_version");
  onlyKeys(
    doc,
    ["schema_version", "name", "seeds", "color_overrides", "knobs"],
    "unknown-field",
    [],
  );
  const layer = emptyLayer();

  if (has(doc, "name") && !acceptableName(doc.name)) refuse("invalid-name", "name");

  if (has(doc, "seeds")) {
    const seeds = doc.seeds;
    if (!isPlainObject(seeds)) refuse("invalid-seeds", "seeds");
    onlyKeys(seeds, ["accent", "neutral", "mood"], "unknown-field", ["seeds"]);
    for (const key of ["accent", "neutral"] as const) {
      if (!has(seeds, key)) continue;
      const raw = seeds[key];
      const color = typeof raw === "string" && SEED_PATTERN.test(raw) ? parseThemeHex(raw) : null;
      if (!color) refuse("invalid-color", "seeds", key);
      layer[key] = color;
    }
    if (has(seeds, "mood")) {
      const mood = seeds.mood;
      if (typeof mood !== "string" || !MOODS.includes(mood))
        refuse("invalid-mood", "seeds", "mood");
      layer.mood = mood;
    }
  }

  if (has(doc, "color_overrides")) {
    const overrides = doc.color_overrides;
    if (!isPlainObject(overrides)) refuse("invalid-overrides", "color_overrides");
    onlyKeys(overrides, THEME_SCHEMES, "unknown-scheme", ["color_overrides"]);
    for (const scheme of THEME_SCHEMES) {
      if (!has(overrides, scheme)) continue;
      const map = overrides[scheme];
      if (!isPlainObject(map)) refuse("invalid-overrides", "color_overrides", scheme);
      layer.overrides[scheme] = readRoleColors(map, THEME_ROLES, ["color_overrides", scheme]);
    }
  }

  if (has(doc, "knobs")) {
    const knobs = doc.knobs;
    if (!isPlainObject(knobs)) refuse("invalid-knobs", "knobs");
    onlyKeys(knobs, KNOB_NAMES, "unknown-field", ["knobs"]);
    for (const [name, values] of KNOB_VALUES) {
      if (!has(knobs, name)) continue;
      const value = knobs[name];
      if (typeof value !== "string" || !values.includes(value))
        refuse("invalid-knob", "knobs", name);
      layer.knobs[name] = value;
    }
  }
  return layer;
}

function readDevice(device: unknown): Partial<Record<KnobName, string>> {
  if (device === undefined) return {};
  if (!isPlainObject(device)) refuse("invalid-device", "device");
  onlyKeys(device, DEVICE_KNOBS, "unknown-field", ["device"]);
  const picked: Partial<Record<KnobName, string>> = {};
  for (const name of DEVICE_KNOBS) {
    if (!has(device, name)) continue;
    const value = device[name];
    const allowed = KNOB_VALUES.find(([knob]) => knob === name)?.[1] ?? [];
    if (typeof value !== "string" || !allowed.includes(value))
      refuse("invalid-knob", "device", name);
    picked[name] = value;
  }
  return picked;
}

// ---------------------------------------------------------------------------------------------
// Layering and derivation

interface MergedLayers {
  accent?: Rgba;
  neutral?: Rgba;
  mood?: string;
  overrides: Record<ThemeScheme, RoleColors>;
  knobs: Partial<Record<KnobName, string>>;
}

function mergeLayers(layers: readonly ThemeLayer[]): MergedLayers {
  const merged: MergedLayers = { overrides: { dark: {}, light: {} }, knobs: {} };
  for (const layer of layers) {
    for (const scheme of THEME_SCHEMES) {
      const bucket = merged.overrides[scheme];
      // A seed claims its whole family: earlier hand-set values for it stop applying.
      if (layer.accent) for (const role of ACCENT_FAMILY) delete bucket[role];
      if (layer.neutral) for (const role of NEUTRAL_FAMILY) delete bucket[role];
    }
    if (layer.accent) merged.accent = layer.accent;
    if (layer.neutral) merged.neutral = layer.neutral;
    if (layer.mood !== undefined) merged.mood = layer.mood;
    Object.assign(merged.knobs, layer.knobs);
    for (const scheme of THEME_SCHEMES) {
      Object.assign(merged.overrides[scheme], layer.overrides[scheme]);
    }
  }
  return merged;
}

function derivePalette(base: Palette, merged: MergedLayers): Palette {
  const factor = MOOD_CHROMA[merged.mood ?? "balanced"] ?? { neutral: 1, accent: 1 };
  const palette: Palette = { ...base };
  const seed = merged.neutral ? oklch(merged.neutral) : null;
  for (const [role, share] of NEUTRAL_SHARE) {
    const source = base[role];
    const lch = oklch(source);
    if (seed) {
      const chroma = Math.min(seed.c * share, NEUTRAL_CHROMA_LIMIT) * factor.neutral;
      palette[role] = fromLch({ l: lch.l, c: chroma, h: seed.h }, source.a);
    } else if (factor.neutral !== 1) {
      palette[role] = fromLch({ l: lch.l, c: lch.c * factor.neutral, h: lch.h }, source.a);
    }
  }
  if (merged.accent || factor.accent !== 1) {
    const lch = oklch(merged.accent ?? base.accent);
    const fill = fromLch({ l: lch.l, c: lch.c * factor.accent, h: lch.h }, 255);
    palette.accent = fill;
    palette["accent-on-light"] = { ...fill };
  }
  return palette;
}

function inkFor(fill: Rgba): Rgba {
  return contrastOf(INK_LIGHT, fill) > contrastOf(INK_DARK, fill)
    ? { ...INK_LIGHT }
    : { ...INK_DARK };
}

// ---------------------------------------------------------------------------------------------
// Contrast repair

interface PairingRow {
  role: ThemeRole;
  against: readonly ThemeRole[];
  floor: number;
}

function textAccentOf(scheme: ThemeScheme): ThemeRole {
  return scheme === "dark" ? "accent" : "accent-on-light";
}

function pairingRows(scheme: ThemeScheme, highContrast: boolean): PairingRow[] {
  const text = highContrast ? 7 : 4.5;
  const graphic = highContrast ? 4.5 : 3;
  const onSurfaces = (role: ThemeRole): PairingRow => ({
    role,
    against: SURFACE_ROLES,
    floor: text,
  });
  const rows: PairingRow[] = [
    onSurfaces("text"),
    onSurfaces("text-muted"),
    onSurfaces(textAccentOf(scheme)),
    onSurfaces("state-direct"),
    onSurfaces("state-remote"),
    onSurfaces("state-restricted"),
    onSurfaces("state-warning"),
    onSurfaces("state-danger"),
  ];
  if (highContrast) rows.push({ role: "line", against: ["bg-root", "bg-surface"], floor: graphic });
  rows.push(
    { role: "border-strong", against: ["bg-root", "bg-surface"], floor: graphic },
    { role: "accent-ink", against: ["accent"], floor: text },
    { role: "focus-ring", against: SURFACE_ROLES, floor: graphic },
  );
  return rows;
}

function meetsFloor(color: Rgba, backdrops: readonly Rgba[], floor: number): boolean {
  return backdrops.every((bg) => contrastOf(color, bg) >= floor);
}

/** Nearest passing OKLCH lightness at 0.001 resolution, hue/chroma/alpha kept; null if none. */
function nearestPassing(color: Rgba, backdrops: readonly Rgba[], floor: number): Rgba | null {
  const lch = oklch(color);
  const first = backdrops[0];
  const away = first !== undefined && oklch(first).l <= lch.l ? 1 : -1;
  const origin = Math.round(lch.l * 1000);
  for (let distance = 1; distance <= 1000; distance++) {
    for (const step of [origin + away * distance, origin - away * distance]) {
      if (step < 0 || step > 1000) continue;
      const candidate = fromLch({ l: step / 1000, c: lch.c, h: lch.h }, color.a);
      if (meetsFloor(candidate, backdrops, floor)) return candidate;
    }
  }
  return null;
}

interface Follow {
  ring: boolean;
  ink: boolean;
}

function repairPalette(
  palette: Palette,
  scheme: ThemeScheme,
  highContrast: boolean,
  follow: Follow,
  log: ThemeRepair[],
): void {
  for (const role of SURFACE_ROLES) {
    const color = palette[role];
    if (color.a === 255) continue;
    const solid = { ...color, a: 255 };
    log.push({
      scheme,
      role,
      from: formatThemeHex(color),
      to: formatThemeHex(solid),
      reason: "surface-opaque",
    });
    palette[role] = solid;
  }
  const [low, high] = SURFACE_LIGHTNESS[scheme];
  for (const role of SURFACE_ROLES) {
    const color = palette[role];
    const lch = oklch(color);
    const bounded = Math.min(high, Math.max(low, lch.l));
    if (bounded === lch.l) continue;
    const moved = fromLch({ l: bounded, c: lch.c, h: lch.h }, 255);
    log.push({
      scheme,
      role,
      from: formatThemeHex(color),
      to: formatThemeHex(moved),
      reason: "surface-range",
    });
    palette[role] = moved;
  }
  if (palette.accent.a !== 255) {
    const seen = flatten(palette.accent, palette["bg-surface"]);
    log.push({
      scheme,
      role: "accent",
      from: formatThemeHex(palette.accent),
      to: formatThemeHex(seen),
      reason: "fill-opaque",
    });
    palette.accent = seen;
  }

  for (const row of pairingRows(scheme, highContrast)) {
    if (row.role === "focus-ring" && follow.ring) {
      palette["focus-ring"] = { ...palette[textAccentOf(scheme)], a: palette["focus-ring"].a };
    }
    if (row.role === "accent-ink" && follow.ink) palette["accent-ink"] = inkFor(palette.accent);
    const backdrops = row.against.map((role) => palette[role]);
    const before = palette[row.role];
    if (meetsFloor(before, backdrops, row.floor)) continue;
    let after = nearestPassing(before, backdrops, row.floor);
    if (!after && before.a !== 255)
      after = nearestPassing({ ...before, a: 255 }, backdrops, row.floor);
    if (!after && row.role === "accent-ink") {
      const ink = inkFor(palette.accent);
      const fill = nearestPassing(palette.accent, [ink], row.floor);
      if (fill) {
        log.push({
          scheme,
          role: "accent",
          from: formatThemeHex(palette.accent),
          to: formatThemeHex(fill),
          reason: "contrast",
          floor: row.floor,
          against: ["accent-ink"],
        });
        palette.accent = fill;
        after = ink;
      }
    }
    if (!after) throw new Error(`theme resolver: no passing value for ${scheme} ${row.role}`);
    if (formatThemeHex(after) === formatThemeHex(before)) continue;
    log.push({
      scheme,
      role: row.role,
      from: formatThemeHex(before),
      to: formatThemeHex(after),
      reason: "contrast",
      floor: row.floor,
      against: [...row.against],
    });
    palette[row.role] = after;
  }
}

// ---------------------------------------------------------------------------------------------
// Bases

/** The token release's theme maps (`theme.cinema`, `theme.workbench`) as published. */
export interface TokenThemeSource {
  cinema: Record<string, string>;
  workbench: Record<string, string>;
}

function basePalette(
  source: Record<string, string>,
  siblings: Partial<Record<ThemeRole, ThemeRole>>,
): Palette {
  const out: Partial<Palette> = {};
  for (const role of THEME_ROLES) {
    const sibling = siblings[role];
    const raw = source[role] ?? (sibling === undefined ? undefined : source[sibling]);
    const color = raw === undefined ? null : parseTokenColor(raw);
    if (!color) throw new Error(`theme base is missing a parseable ${role}`);
    out[role] = color;
  }
  return out as Palette;
}

/**
 * Complete the dark (cinema) and light (workbench) bases from a token release: missing dark
 * roles take their sibling, then each base passes through one repair whose log is discarded.
 */
export function compileBasePalettes(theme: TokenThemeSource): BasePalettes {
  const bases: BasePalettes = {
    dark: basePalette(theme.cinema, { "border-strong": "line", "accent-on-light": "accent" }),
    light: basePalette(theme.workbench, {}),
  };
  for (const scheme of THEME_SCHEMES) {
    repairPalette(bases[scheme], scheme, false, { ring: false, ink: false }, []);
  }
  return bases;
}

/** Bases from canonical hex maps (as a generator emits them). */
export function basePalettesFromHex(hex: Record<ThemeScheme, HexPalette>): BasePalettes {
  const read = (map: HexPalette): Palette => {
    const out: Partial<Palette> = {};
    for (const role of THEME_ROLES) {
      const color = parseThemeHex(map[role]);
      if (!color) throw new Error(`theme base has an invalid ${role}`);
      out[role] = color;
    }
    return out as Palette;
  };
  return { dark: read(hex.dark), light: read(hex.light) };
}

export function paletteToHex(palette: Palette): HexPalette {
  const out: Partial<HexPalette> = {};
  for (const role of THEME_ROLES) out[role] = formatThemeHex(palette[role]);
  return out as HexPalette;
}

// ---------------------------------------------------------------------------------------------
// Resolution

function resolveKnobs(
  themeKnobs: Partial<Record<KnobName, string>>,
  device: Partial<Record<KnobName, string>>,
  options: ResolveOptions,
): { knobs: ThemeKnobs; scales: ThemeScales } {
  const pick = (name: KnobName): string => {
    const fallback = KNOB_VALUES.find(([knob]) => knob === name)?.[1][0] ?? "";
    return device[name] ?? themeKnobs[name] ?? fallback;
  };
  let motion = pick("motion");
  if (options.reduceMotion && motion === "full") motion = "reduced";
  const material = options.highContrast || options.reduceTransparency ? "solid" : pick("material");
  const knobs: ThemeKnobs = {
    shape: pick("shape"),
    density: pick("density"),
    typeScale: pick("type_scale"),
    typeface: pick("typeface"),
    motion,
    material,
    artworkTint: pick("artwork_tint"),
  };
  return {
    knobs,
    scales: {
      radius: RADIUS_SCALE[knobs.shape] ?? 1,
      spacing: SPACING_SCALE[knobs.density] ?? 1,
      type: TYPE_SCALE[knobs.typeScale] ?? 1,
    },
  };
}

/**
 * Resolve theme layers (earliest first: server default, person, accent layer) over `base`.
 * Returns the resolved theme, or `{ error: { code, path, layer } }` for the first violation
 * (device adjustments are checked first and report layer -1).
 */
export function resolveTheme(
  layers: readonly unknown[],
  options: ResolveOptions,
  base: BasePalettes,
): ResolvedTheme | ThemeRejection {
  let device: Partial<Record<KnobName, string>>;
  try {
    device = readDevice(options.device);
  } catch (e) {
    if (e instanceof Rejection) return { error: { code: e.code, path: e.path, layer: -1 } };
    throw e;
  }
  const parsed: ThemeLayer[] = [];
  for (let index = 0; index < layers.length; index++) {
    try {
      parsed.push(readLayer(layers[index]));
    } catch (e) {
      if (e instanceof Rejection) return { error: { code: e.code, path: e.path, layer: index } };
      throw e;
    }
  }
  const merged = mergeLayers(parsed);
  const { knobs, scales } = resolveKnobs(merged.knobs, device, options);
  const repairs: ThemeRepair[] = [];
  const output: Partial<Record<ThemeScheme, HexPalette>> = {};
  for (const scheme of THEME_SCHEMES) {
    const overrides = merged.overrides[scheme];
    const palette = derivePalette(base[scheme], merged);
    Object.assign(palette, overrides);
    if (options.reduceTransparency) {
      for (const role of THEME_ROLES) {
        if (SURFACE_ROLES.includes(role) || palette[role].a === 255) continue;
        palette[role] = flatten(palette[role], palette["bg-surface"]);
      }
    }
    const follow: Follow = {
      ring: overrides["focus-ring"] === undefined,
      ink: merged.accent !== undefined && overrides["accent-ink"] === undefined,
    };
    repairPalette(palette, scheme, options.highContrast === true, follow, repairs);
    output[scheme] = paletteToHex(palette);
  }
  return {
    schemaVersion: THEME_SCHEMA_VERSION,
    dark: output.dark as HexPalette,
    light: output.light as HexPalette,
    knobs,
    scales,
    repairs,
  };
}
