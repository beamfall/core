import { createContext, type ReactNode, useContext, useMemo, useState } from "react";
import { type Locale, STRINGS, type StringKey } from "@/strings";

// Localization (ADR-0034). Strings render as TEXT ONLY, never HTML
// (ADR-0012 §5) — React's default {expression} escaping enforces this, so a
// hostile string can never inject markup. Missing keys fall back to `en`.

interface I18nValue {
  locale: Locale;
  displayLocale: string;
  setLocale: (locale: Locale) => void;
  t: (key: StringKey, vars?: Record<string, string | number>) => string;
}

const I18nContext = createContext<I18nValue | null>(null);
const SUPPORTED_LOCALES: readonly Locale[] = ["en", "fr", "de", "es", "pt"];

export interface LocalePreference {
  locale: Locale;
  displayLocale: string;
}

function isLocale(value: string): value is Locale {
  return (SUPPORTED_LOCALES as readonly string[]).includes(value);
}

export function resolveLocalePreference(
  languages: readonly string[] | undefined,
): LocalePreference {
  for (const tag of languages || []) {
    const displayLocale = tag.trim();
    if (!displayLocale) continue;
    const lower = displayLocale.toLowerCase();
    if (isLocale(lower)) return { locale: lower, displayLocale };
    const primary = lower.split("-")[0];
    if (isLocale(primary)) return { locale: primary, displayLocale };
  }
  const displayLocale = (languages || []).find((tag) => tag.trim() !== "")?.trim() ?? "en";
  return { locale: "en", displayLocale };
}

function resolveBrowserLocalePreference(): LocalePreference {
  if (typeof navigator === "undefined") {
    return { locale: "en", displayLocale: "en" };
  }
  const languages =
    navigator.languages && navigator.languages.length > 0
      ? navigator.languages
      : [navigator.language];
  return resolveLocalePreference(languages);
}

// Locale-aware message formatting (ADR-0034). A superset of the historical
// `{name}` interpolation that stays text-only (ADR-0012 §5 — React escaping
// still applies to the returned string) and is backward compatible: a plain
// `{name}` placeholder renders exactly as before. Two ICU-lite forms add
// locale awareness *only when a string opts in* — no existing catalog string
// uses them, so rendering is unchanged until a string adopts the syntax:
//
//   {count, number}                      -> Intl.NumberFormat(locale)
//   {count, plural, =0 {none}            -> Intl.PluralRules(locale) category,
//                   one {# item}            with `#` = the formatted number and
//                   other {# items}}        exact `=N` selectors taking priority
//
// A leading apostrophe quotes literals: `''` -> `'`, and `'{'` / `'}'` / `'#'`
// render the syntax character literally.
function formatNumberFor(locale: string, value: number): string {
  try {
    return new Intl.NumberFormat(locale || "en").format(value);
  } catch {
    return new Intl.NumberFormat("en").format(value);
  }
}

function selectPluralCategory(locale: string, value: number): Intl.LDMLPluralRule {
  try {
    return new Intl.PluralRules(locale || "en").select(value);
  } catch {
    return new Intl.PluralRules("en").select(value);
  }
}

/** Read a `{...}` group starting at `start` (must be `{`), honoring nested
 *  braces and apostrophe quotes. Returns the inner text and the index past `}`. */
function readGroup(str: string, start: number): { content: string; end: number } {
  let depth = 0;
  for (let i = start; i < str.length; i++) {
    const c = str[i];
    if (c === "'") {
      if (str[i + 1] === "'") {
        i++;
        continue;
      }
      i++;
      while (i < str.length && str[i] !== "'") i++;
      continue;
    }
    if (c === "{") depth++;
    else if (c === "}") {
      depth--;
      if (depth === 0) return { content: str.slice(start + 1, i), end: i + 1 };
    }
  }
  return { content: str.slice(start + 1), end: str.length };
}

/** Split on top-level commas only (depth 0, outside quotes). */
function splitTopLevel(str: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let current = "";
  for (let i = 0; i < str.length; i++) {
    const c = str[i];
    if (c === "'") {
      current += c;
      if (str[i + 1] === "'") {
        current += str[i + 1];
        i++;
        continue;
      }
      i++;
      while (i < str.length && str[i] !== "'") current += str[i++];
      if (i < str.length) current += str[i];
      continue;
    }
    if (c === "{") depth++;
    else if (c === "}") depth--;
    if (c === "," && depth === 0) {
      parts.push(current);
      current = "";
      continue;
    }
    current += c;
  }
  parts.push(current);
  return parts;
}

function parsePluralOptions(str: string): Record<string, string> {
  const options: Record<string, string> = {};
  let i = 0;
  while (i < str.length) {
    while (i < str.length && /\s/.test(str[i])) i++;
    let selector = "";
    while (i < str.length && str[i] !== "{") selector += str[i++];
    if (str[i] !== "{") break;
    const { content, end } = readGroup(str, i);
    options[selector.trim()] = content;
    i = end;
  }
  return options;
}

function formatArg(content: string, vars: Record<string, string | number>, locale: string): string {
  const parts = splitTopLevel(content);
  const name = parts[0].trim();
  if (!(name in vars)) return `{${content}}`;
  const type = parts[1]?.trim();
  if (type === "number") return formatNumberFor(locale, Number(vars[name]));
  if (type === "plural") {
    const value = Number(vars[name]);
    const options = parsePluralOptions(parts.slice(2).join(","));
    const branch =
      options[`=${value}`] ?? options[selectPluralCategory(locale, value)] ?? options.other ?? "";
    return formatMessageICU(branch, vars, locale, value);
  }
  return String(vars[name]);
}

function formatMessageICU(
  template: string,
  vars: Record<string, string | number> | undefined,
  locale: string,
  hashValue?: number,
): string {
  let out = "";
  for (let i = 0; i < template.length; ) {
    const c = template[i];
    if (c === "'") {
      if (template[i + 1] === "'") {
        out += "'";
        i += 2;
        continue;
      }
      let j = i + 1;
      let literal = "";
      while (j < template.length && template[j] !== "'") literal += template[j++];
      out += literal;
      i = j < template.length ? j + 1 : j;
      continue;
    }
    if (c === "#" && hashValue !== undefined) {
      out += formatNumberFor(locale, hashValue);
      i++;
      continue;
    }
    if (c === "{" && vars) {
      const { content, end } = readGroup(template, i);
      out += formatArg(content, vars, locale);
      i = end;
      continue;
    }
    out += c;
    i++;
  }
  return out;
}

// `TYPED_ARG` detects an opt-in typed argument (`{name, number}` /
// `{name, plural, ...}`): a `{`, a name, then a comma before the group closes.
// Legacy strings only ever use bare `{name}` placeholders (no comma inside the
// braces) and free-standing apostrophes, so they take the simple, byte-for-byte
// unchanged interpolation path — the ICU-lite parser (with its apostrophe
// quoting rules) engages only when a string deliberately adopts the new syntax.
const TYPED_ARG = /\{[^{}]*,/;

export function formatMessage(
  template: string,
  vars: Record<string, string | number> | undefined,
  locale: string,
): string {
  if (!vars) return template;
  if (!TYPED_ARG.test(template)) {
    return template.replace(/\{(\w+)\}/g, (match, name: string) =>
      name in vars ? String(vars[name]) : match,
    );
  }
  return formatMessageICU(template, vars, locale);
}

export function I18nProvider({
  children,
  initialLocale,
  initialDisplayLocale,
}: {
  children: ReactNode;
  initialLocale?: Locale;
  initialDisplayLocale?: string;
}) {
  const [preference, setPreference] = useState<LocalePreference>(() => {
    if (initialLocale) {
      return {
        locale: initialLocale,
        displayLocale: initialDisplayLocale ?? initialLocale,
      };
    }
    return resolveBrowserLocalePreference();
  });

  const value = useMemo<I18nValue>(() => {
    const table = STRINGS[preference.locale] ?? STRINGS.en;
    return {
      locale: preference.locale,
      displayLocale: preference.displayLocale,
      setLocale,
      t: (key, vars) => {
        const raw = table[key] ?? STRINGS.en[key] ?? String(key);
        return formatMessage(raw, vars, preference.displayLocale);
      },
    };
  }, [preference]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;

  function setLocale(locale: Locale) {
    setPreference({ locale, displayLocale: locale });
  }
}

export function useI18n(): I18nValue {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n must be used within <I18nProvider>");
  return ctx;
}

/** Shorthand: const t = useT(); t("appTitle"). */
export function useT(): I18nValue["t"] {
  return useI18n().t;
}

export function useDisplayLocale(): string {
  return useI18n().displayLocale;
}
