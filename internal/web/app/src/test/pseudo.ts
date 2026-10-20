// Pseudo-locale text expansion (ADR-0034 Decision 8 — text-expansion layout
// gate; docs/design/text-expansion-gate.md). German and the Romance launch
// languages routinely run 30–50% longer than English; a UI that fits English
// can clip / truncate-lossily / overlap once translated. Rather than wait for
// real translations to regress a layout, we pseudo-localize: take the English
// catalog and synthesize a *longest-plausible* string — accented (to surface
// font / encoding gaps), padded to a target length, and bracketed so a visual
// truncation is detectable — then render the shared surface set against it.
//
// This is the same technique Apple/Android use (Xcode's "Double-Length
// Pseudolanguage", Android's pseudolocales en-XA). The ratios below are the
// contract values: LAUNCH is the documented launch ceiling, STRESS is the
// longest-plausible pass the gate asserts against.

/** Documented launch ceiling — German/Romance run ~30–50% longer (ADR-0034 §Context, Decision 8). */
export const LAUNCH_EXPANSION = 1.5;

/** Longest-plausible stress factor the gate asserts against (headroom over the launch ceiling). */
export const STRESS = 2.0;

// Latin-1/accent substitution. Pure cosmetic — same character count — so it
// never changes length on its own; the padding below is what drives expansion.
const ACCENT: Record<string, string> = {
  a: "á",
  b: "ƀ",
  c: "ç",
  d: "ð",
  e: "é",
  f: "ƒ",
  g: "ĝ",
  h: "ĥ",
  i: "í",
  j: "ĵ",
  k: "ķ",
  l: "ļ",
  m: "ɱ",
  n: "ñ",
  o: "ó",
  p: "þ",
  q: "ɋ",
  r: "ŕ",
  s: "š",
  t: "ţ",
  u: "ú",
  v: "ṽ",
  w: "ŵ",
  x: "ẋ",
  y: "ý",
  z: "ž",
  A: "Á",
  B: "Ɓ",
  C: "Ç",
  D: "Ð",
  E: "É",
  F: "Ƒ",
  G: "Ĝ",
  H: "Ĥ",
  I: "Í",
  J: "Ĵ",
  K: "Ķ",
  L: "Ļ",
  M: "Ṁ",
  N: "Ñ",
  O: "Ó",
  P: "Þ",
  Q: "Ɋ",
  R: "Ŕ",
  S: "Š",
  T: "Ţ",
  U: "Ú",
  V: "Ṽ",
  W: "Ŵ",
  X: "Ẋ",
  Y: "Ý",
  Z: "Ž",
};

// Interpolation placeholders (`{count}`, `{performer}`) MUST survive untouched —
// they are replaced at render time by i18n.tsx#interpolate, and accenting or
// padding inside them would break the substitution (and is exactly the
// bidi-isolation surface ADR-0034 Decision 7 reserves for later).
const PLACEHOLDER = /(\{\w+\})/g;
const IS_PLACEHOLDER = /^\{\w+\}$/;

function accent(s: string): string {
  return Array.from(s, (ch) => ACCENT[ch] ?? ch).join("");
}

/**
 * Expand one string to ~`ratio`× its visible length: accent the literal text,
 * preserve `{placeholders}`, pad to the target, and bracket with ⟦ ⟧ so a
 * downstream visual clip removes the trailing bracket (a detectable signal in
 * the real-browser arm). Empty strings pass through unchanged.
 */
export function expandText(s: string, ratio: number = STRESS): string {
  if (!s) return s;
  const accented = s
    .split(PLACEHOLDER)
    .map((part) => (IS_PLACEHOLDER.test(part) ? part : accent(part)))
    .join("");
  const target = Math.ceil(s.length * ratio);
  const padLen = Math.max(0, target - accented.length);
  const pad = padLen > 0 ? " " + "·".repeat(padLen) : "";
  return `⟦${accented}${pad}⟧`;
}

/**
 * Build an expanded copy of a keyed catalog table (every value pseudo-expanded).
 * Used to inject `STRINGS.<locale> = expandStrings(STRINGS.en)` in the gate.
 */
export function expandStrings<T extends Record<string, string>>(
  table: T,
  ratio: number = STRESS,
): T {
  return Object.fromEntries(
    Object.entries(table).map(([key, value]) => [key, expandText(value, ratio)]),
  ) as T;
}

/**
 * Expand a manifest display-string (ADR-0034 locale map, or its string
 * shorthand). A map keeps its keys; each entry value is expanded. Mirrors the
 * shape `displayString()` resolves so the gate exercises the locale-map path
 * (not just the keyed catalog).
 */
export function expandDisplayString(
  value: string | Record<string, string>,
  ratio: number = STRESS,
): string | Record<string, string> {
  if (typeof value === "string") return expandText(value, ratio);
  return Object.fromEntries(
    Object.entries(value).map(([locale, str]) => [locale, expandText(str, ratio)]),
  );
}
