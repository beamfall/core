import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
} from "react";
import type { ResolvedTheme, ThemeRejection } from "@/lib/theme/resolver";
import {
  type Appearance,
  applyPalettes,
  applyScheme,
  isPresetId,
  isThemeRejection,
  loadAppearance,
  resolveAppearance,
  type SchemePreference,
  SYSTEM_LIGHT_QUERY,
  saveAppearance,
} from "@/lib/theme/runtime";

// The design system's two visual registers (docs/design-system.md):
//   viewer -> Cinema (the immersive default)
//   manage -> Workbench
// Register, scheme and theme are visible STATE, never a hidden mode — they drive the data-*
// attributes and the --theme-* custom properties on <html> that theme.css keys off.
//
// Theme (ADR-0231 D7, CORE-1179): the device chooses light, dark or system (stored on this
// device only); the Profile chooses a shipped preset (theme.preset in profile_client_prefs,
// synced by ThemePrefsSync). A theme document can be applied at run time in place of the
// preset; the app re-skins without a reload.

export type Register = "viewer" | "manage";
export type Theme = "dark" | "light";

interface RegisterValue {
  register: Register;
  /** The scheme on screen now (the device preference, with "system" resolved). */
  theme: Theme;
  setRegister: (register: Register) => void;
  /** Sets the device scheme preference to an explicit light or dark. */
  setTheme: (theme: Theme) => void;
  schemePreference: SchemePreference;
  setSchemePreference: (preference: SchemePreference) => void;
  preset: string;
  /** Applies a shipped preset (clearing any applied theme document). Unknown ids are ignored. */
  setPreset: (preset: string) => void;
  /**
   * Resolves `document` and applies it in place of the preset. A rejected document leaves the
   * current theme on screen and returns the rejection.
   */
  applyThemeDocument: (document: unknown) => ResolvedTheme | ThemeRejection;
}

const RegisterContext = createContext<RegisterValue | null>(null);

function useSystemLight(): boolean {
  const query = () =>
    typeof window.matchMedia === "function" && window.matchMedia(SYSTEM_LIGHT_QUERY).matches;
  const [light, setLight] = useState(query);
  useEffect(() => {
    if (typeof window.matchMedia !== "function") return;
    const list = window.matchMedia(SYSTEM_LIGHT_QUERY);
    const onChange = () => setLight(list.matches);
    list.addEventListener?.("change", onChange);
    return () => list.removeEventListener?.("change", onChange);
  }, []);
  return light;
}

export function RegisterProvider({ children }: { children: ReactNode }) {
  const [register, setRegister] = useState<Register>("viewer");
  const [appearance, setAppearance] = useState<Appearance>(() => loadAppearance());
  const [document_, setDocument] = useState<{ value: unknown } | null>(null);
  const systemLight = useSystemLight();

  const theme: Theme =
    appearance.scheme === "system" ? (systemLight ? "light" : "dark") : appearance.scheme;

  const update = useCallback((next: Partial<Appearance>) => {
    setAppearance((current) => {
      const merged = { ...current, ...next };
      if (merged.scheme === current.scheme && merged.preset === current.preset) return current;
      saveAppearance(merged);
      return merged;
    });
  }, []);

  const setSchemePreference = useCallback(
    (scheme: SchemePreference) => update({ scheme }),
    [update],
  );
  const setTheme = useCallback((next: Theme) => update({ scheme: next }), [update]);
  const setPreset = useCallback(
    (preset: string) => {
      if (!isPresetId(preset)) return;
      setDocument(null);
      update({ preset });
    },
    [update],
  );
  const applyThemeDocument = useCallback((value: unknown) => {
    const resolved = resolveAppearance({ preset: "", document: value });
    if (!isThemeRejection(resolved)) setDocument({ value });
    return resolved;
  }, []);

  useLayoutEffect(() => {
    document.documentElement.dataset.register = register;
  }, [register]);

  useLayoutEffect(() => {
    applyScheme(theme);
  }, [theme]);

  useLayoutEffect(() => {
    const resolved = resolveAppearance(
      document_ ? { preset: appearance.preset, document: document_.value } : appearance,
    );
    // A stored document was accepted when applied; presets always resolve.
    if (!isThemeRejection(resolved)) applyPalettes(resolved);
  }, [appearance, document_]);

  const value = useMemo<RegisterValue>(
    () => ({
      register,
      theme,
      setRegister,
      setTheme,
      schemePreference: appearance.scheme,
      setSchemePreference,
      preset: appearance.preset,
      setPreset,
      applyThemeDocument,
    }),
    [register, theme, appearance, setTheme, setSchemePreference, setPreset, applyThemeDocument],
  );

  return <RegisterContext.Provider value={value}>{children}</RegisterContext.Provider>;
}

export function useRegister(): RegisterValue {
  const ctx = useContext(RegisterContext);
  if (!ctx) throw new Error("useRegister must be used within <RegisterProvider>");
  return ctx;
}
