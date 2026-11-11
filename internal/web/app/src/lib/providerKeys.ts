import type { PluginScraperInfo } from "@/lib/graphql/types";

/** One AuthRef-scoped scraper credential, presented as a "provider key". */
export interface ProviderKey {
  authRef: string;
  /** Human label derived from the plugin's own scraper display names. */
  label: string;
  /** Endpoint hosts the key is sent to. */
  hosts: string[];
  required: boolean;
  status: string;
  error?: string;
  pluginIDs: string[];
}

// Drop a trailing parenthetical qualifier: "TMDB (Movies)" -> "TMDB".
function stripQualifier(name: string): string {
  return name.replace(/\s*\([^()]*\)\s*$/, "").trim();
}

/**
 * Label a credential from the scrapers that share it. Core knows no provider
 * names, so the label comes from plugin data: the shared base of the scraper
 * display names, falling back to the AuthRef.
 */
export function providerKeyLabel(names: string[], authRef: string): string {
  const bases = names.map(stripQualifier).filter(Boolean);
  if (bases.length === 0) return authRef;
  if (bases.every((base) => base === bases[0])) return bases[0];
  let prefix = bases[0];
  for (const base of bases.slice(1)) {
    while (prefix && !base.startsWith(prefix)) prefix = prefix.slice(0, -1);
  }
  prefix = prefix.replace(/[\s\-–—:/(]+$/, "").trim();
  return prefix.length >= 2 ? prefix : bases[0];
}

function endpointHost(endpoint: string): string {
  try {
    return new URL(endpoint).host || endpoint;
  } catch {
    return endpoint;
  }
}

/** Group scrapers by AuthRef, keeping the first-seen order. */
export function groupProviderKeys(scrapers: PluginScraperInfo[]): ProviderKey[] {
  const grouped = new Map<string, PluginScraperInfo[]>();
  for (const scraper of scrapers) {
    if (!scraper.authRef) continue;
    const rows = grouped.get(scraper.authRef) ?? [];
    rows.push(scraper);
    grouped.set(scraper.authRef, rows);
  }
  return [...grouped.entries()].map(([authRef, rows]) => {
    const first = rows[0];
    return {
      authRef,
      label: providerKeyLabel(
        rows.map((row) => row.displayName || ""),
        authRef,
      ),
      hosts: [...new Set(rows.map((row) => endpointHost(row.endpoint)).filter(Boolean))],
      required: rows.some((row) => row.credentialRequired),
      status: first.credentialStatus || "unavailable",
      error: rows.find((row) => row.credentialError)?.credentialError,
      pluginIDs: [...new Set(rows.map((row) => row.pluginID))],
    };
  });
}

/** True when a required key has not been configured yet. */
export function keyNeeded(key: ProviderKey): boolean {
  return key.required && key.status !== "configured";
}
