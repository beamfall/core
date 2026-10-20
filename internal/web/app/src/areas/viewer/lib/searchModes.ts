// Which search modes the viewer may expose (ADR-0083 §30): a mode appears ONLY
// when the server reports it available to this reveal-filtered session, so the
// viewer never offers a mode it can't serve.
//
// - keyword: always available (the FTS floor, ADR-0041).
// - semantic: available iff any visible Library has the enrichment plugin
//   active. This mirrors the vanilla app's applySemanticCapability exactly
//   (caps.some(c => c.pluginActive)); the per-Library `state` is informational
//   and not needed to gate the toggle (an active plugin with an empty index
//   degrades to keyword server-side and we surface that honestly at query time).
// - recommend: available iff the Profile has opted in (recommend.opt-in
//   clientPref, ADR-0053 §2.4) — surfaced/edited on the Profile Settings page.

import type { ClientPref, SearchMode, SemanticCapabilityInfo } from "@/lib/graphql/types";
import { PREF_KEYS, PREF_ON } from "./preferences";

/** True when semantic search is available to this session. */
export function semanticAvailable(caps: SemanticCapabilityInfo[]): boolean {
  return caps.some((c) => c.pluginActive);
}

/** True when the Profile has opted in to recommend mode. */
export function recommendAvailable(prefs: ClientPref[]): boolean {
  return prefs.some((p) => p.key === PREF_KEYS.recommendOptIn && p.value === PREF_ON);
}

/**
 * availableModes returns the ordered list of selectable modes. keyword is
 * always present and first; semantic and recommend are appended only when
 * available. A single-element result means the mode toggle should be hidden
 * (keyword-only, the unchanged baseline UX).
 */
export function availableModes(caps: SemanticCapabilityInfo[], prefs: ClientPref[]): SearchMode[] {
  const modes: SearchMode[] = ["keyword"];
  if (semanticAvailable(caps)) modes.push("semantic");
  if (recommendAvailable(prefs)) modes.push("recommend");
  return modes;
}
