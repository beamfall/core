import type { ArtworkCandidate } from "@/lib/graphql/types";

// Only relative same-origin preview URLs render an <img> (app.js
// isRelativePreviewURL) — artwork bytes are served by the server at
// /api/media/artwork-candidate/...; an absolute or protocol-relative URL is
// never followed.
export function isRelativePreviewURL(value: string | undefined): value is string {
  return typeof value === "string" && value.startsWith("/") && !value.startsWith("//");
}

/** Where a candidate came from: "plugin / scraper", or "" when unknown. */
export function candidateProvenance(candidate: ArtworkCandidate): string {
  return [candidate.pluginID, candidate.scraperID].filter(Boolean).join(" / ");
}
