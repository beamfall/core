// Run-time theme seam (ADR-0231 D7, CORE-1179). The generated tokens.css carries the default
// preset's resolved roles as --theme-dark-* / --theme-light-*; this module resolves the chosen
// preset (or a theme document) with the app's resolver and writes the same custom properties
// inline on <html>, so the whole app re-skins without a reload. src/styles/theme.css maps them
// onto the role names the styles use.
//
// Device-local: the light/dark/system choice (and a cached copy of the preset, so the first paint
// is already themed). The preset itself roams through profile_client_prefs (theme.preset); see
// src/lib/theme/ThemePrefsSync.tsx.

import {
  basePalettesFromHex,
  type HexPalette,
  isThemeRejection,
  type ResolvedTheme,
  resolveTheme,
  THEME_ROLES,
  type ThemeRejection,
  type ThemeScheme,
} from "./resolver";
import { THEME_BASE_HEX, THEME_DEFAULT_PRESET, THEME_PRESETS } from "./theme.generated";

export type SchemePreference = "dark" | "light" | "system";

export interface Appearance {
  scheme: SchemePreference;
  preset: string;
}

/**
 * Per-Profile theme keys in profile_client_prefs (ADR-0231 D10). theme.accent is reserved for
 * the accent layer that roams with CORE-1184; this build reads and writes theme.preset only.
 */
export const THEME_PREF_KEYS = { preset: "theme.preset", accent: "theme.accent" } as const;

export const APPEARANCE_STORAGE_KEY = "beamfall.web.appearance.v1";
export const SCHEME_PREFERENCES: readonly SchemePreference[] = ["dark", "light", "system"];
export const DEFAULT_APPEARANCE: Appearance = { scheme: "dark", preset: THEME_DEFAULT_PRESET };

const BASE = basePalettesFromHex(THEME_BASE_HEX);

export function isPresetId(value: unknown): value is string {
  return typeof value === "string" && THEME_PRESETS.some((preset) => preset.id === value);
}

const isSchemePreference = (value: unknown): value is SchemePreference =>
  typeof value === "string" && (SCHEME_PREFERENCES as readonly string[]).includes(value);

/** The stored device appearance; unknown or corrupt values fall back field by field. */
export function loadAppearance(storage: Storage | undefined = safeStorage()): Appearance {
  let raw: unknown = null;
  try {
    const text = storage?.getItem(APPEARANCE_STORAGE_KEY);
    raw = text ? JSON.parse(text) : null;
  } catch {
    raw = null;
  }
  const record = raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {};
  return {
    scheme: isSchemePreference(record.scheme) ? record.scheme : DEFAULT_APPEARANCE.scheme,
    preset: isPresetId(record.preset) ? record.preset : DEFAULT_APPEARANCE.preset,
  };
}

export function saveAppearance(
  appearance: Appearance,
  storage: Storage | undefined = safeStorage(),
): void {
  try {
    storage?.setItem(APPEARANCE_STORAGE_KEY, JSON.stringify(appearance));
  } catch {
    // Private mode or a full quota: the choice still applies for this session.
  }
}

function safeStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.localStorage;
  } catch {
    return undefined;
  }
}

function matches(query: string): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function"
    ? window.matchMedia(query).matches
    : false;
}

export const SYSTEM_LIGHT_QUERY = "(prefers-color-scheme: light)";

/** The scheme a preference shows right now ("system" follows the OS). */
export function resolveScheme(preference: SchemePreference): ThemeScheme {
  if (preference === "system") return matches(SYSTEM_LIGHT_QUERY) ? "light" : "dark";
  return preference;
}

/** A shipped preset as a theme layer: its resolved roles, pinned as overrides for both schemes. */
export function presetLayer(id: string): unknown {
  const preset = THEME_PRESETS.find((p) => p.id === id) ?? THEME_PRESETS[0];
  return { schema_version: 3, color_overrides: { dark: preset.dark, light: preset.light } };
}

/**
 * Resolves a preset, or a theme document in its place, with the device's accessibility settings
 * (more contrast, reduced transparency) applied by the resolver.
 */
export function resolveAppearance(source: {
  preset: string;
  document?: unknown;
}): ResolvedTheme | ThemeRejection {
  const layer = source.document === undefined ? presetLayer(source.preset) : source.document;
  return resolveTheme(
    [layer],
    {
      highContrast: matches("(prefers-contrast: more)"),
      reduceTransparency: matches("(prefers-reduced-transparency: reduce)"),
    },
    BASE,
  );
}

/** Writes both schemes' resolved roles onto `root` as the --theme-<scheme>-<role> properties. */
export function applyPalettes(
  palettes: Record<ThemeScheme, HexPalette>,
  root: HTMLElement = document.documentElement,
): void {
  for (const scheme of ["dark", "light"] as const) {
    for (const role of THEME_ROLES) {
      root.style.setProperty(`--theme-${scheme}-${role}`, palettes[scheme][role]);
    }
  }
}

/** Shows `scheme` on `root`: light sets data-theme="light"; dark is the stylesheet default. */
export function applyScheme(scheme: ThemeScheme, root: HTMLElement = document.documentElement) {
  if (scheme === "light") root.dataset.theme = "light";
  else delete root.dataset.theme;
}

export { isThemeRejection };
