import {
  createSmartCollection,
  deleteSmartCollection,
  fetchSmartCollectionItems,
  fetchSmartCollections,
  updateSmartCollection,
} from "@/lib/graphql";
import type { NavigationManifest, SmartCollectionInfo, SourceItem } from "@/lib/graphql/types";

// Smart playlists (CORE-2771) are Smart Collections whose sourceExpr is a rule
// source: {"kind":"rules","match":"all"|"any","rules":[{field,op,value}],"limit"}.
// The server validates and evaluates them on every read for the calling
// Profile, so a smart playlist follows library and play-history changes with
// no refresh step. Field names are generic: built-in Media fields, per-Profile
// engagement, plugin Media fields ("field:<name>") and plugin entity relations
// ("entity:<kind>"). This module only shapes the expressions and the reads.

export type SmartMatch = "all" | "any";
export type SmartSortDir = "asc" | "desc";

/** One rule as the editor holds it: the value stays text until it is serialized. */
export interface SmartRule {
  field: string;
  op: string;
  value: string;
}

export interface SmartRuleSet {
  match: SmartMatch;
  rules: SmartRule[];
  /** Whole match-list cap; null leaves the server's maximum. */
  limit: number | null;
}

export interface SmartSort {
  by: string;
  dir: SmartSortDir;
}

export interface SmartPlaylist {
  id: string;
  libraryID: string;
  name: string;
  rules: SmartRuleSet;
  sort: SmartSort;
}

export type SmartFieldClass = "text" | "number" | "date" | "plugin" | "entity";
export type SmartValueKind = "none" | "text" | "number" | "days" | "date";

export const SMART_RULE_MAX_RULES = 20;
export const SMART_RULE_MAX_LIMIT = 5000;
/** The most items the web page reads for one smart playlist (pages of 200). */
export const SMART_PLAYLIST_READ_CAP = 1000;
const PAGE_SIZE = 200;

export const SMART_BUILTIN_FIELDS = [
  "title",
  "duration",
  "year",
  "playCount",
  "rating",
  "lastPlayed",
] as const;

export const SMART_SORT_KEYS = [
  "title",
  "duration",
  "year",
  "added",
  "playCount",
  "lastPlayed",
  "rating",
  "random",
] as const;

const BUILTIN_CLASS: Record<string, SmartFieldClass> = {
  title: "text",
  duration: "number",
  year: "number",
  playCount: "number",
  rating: "number",
  lastPlayed: "date",
};

const TEXT_OPS = ["is", "isNot", "contains", "notContains", "beginsWith"];
const NUMBER_OPS = ["is", "isNot", "gt", "gte", "lt", "lte"];
const DATE_OPS = ["inLast", "notInLast", "before", "after"];
const FLAG_OPS = ["isTrue", "isFalse"];

/** The field's class, or null for a name the server would reject. */
export function smartFieldClass(field: string): SmartFieldClass | null {
  if (BUILTIN_CLASS[field]) return BUILTIN_CLASS[field];
  if (field.startsWith("field:") && field.slice(6).trim()) return "plugin";
  if (field.startsWith("entity:") && field.slice(7).trim()) return "entity";
  return null;
}

/** The operators the server accepts for a field, in display order. */
export function smartOperators(field: string): string[] {
  switch (smartFieldClass(field)) {
    case "text":
    case "entity":
      return TEXT_OPS;
    case "number":
      return NUMBER_OPS;
    case "date":
      return DATE_OPS;
    case "plugin":
      return [...TEXT_OPS, ...NUMBER_OPS.slice(2), ...DATE_OPS, ...FLAG_OPS];
    default:
      return [];
  }
}

/** What kind of value an operator takes on a field. */
export function smartValueKind(field: string, op: string): SmartValueKind {
  if (FLAG_OPS.includes(op)) return "none";
  if (op === "inLast" || op === "notInLast") return "days";
  if (op === "before" || op === "after") return "date";
  if (smartFieldClass(field) === "number" || NUMBER_OPS.slice(2).includes(op)) return "number";
  return "text";
}

/** A new rule on a field, with the field's first operator. */
export function newSmartRule(field = "title"): SmartRule {
  return { field, op: smartOperators(field)[0] ?? "is", value: "" };
}

/**
 * The rule with a new field: the operator is kept when the new field allows it,
 * and the value is kept when it still means the same kind of thing.
 */
export function withSmartField(rule: SmartRule, field: string): SmartRule {
  const ops = smartOperators(field);
  const op = ops.includes(rule.op) ? rule.op : (ops[0] ?? "is");
  const value = smartValueKind(field, op) === smartValueKind(rule.field, rule.op) ? rule.value : "";
  return { field, op, value };
}

/** Plugin-declared entity kinds and Media fields named by the manifest, for the field menu. */
export function smartFieldOptions(manifest: NavigationManifest | null | undefined): {
  entityKinds: string[];
  pluginFields: string[];
} {
  const kinds = new Set<string>();
  const fields = new Set<string>();
  for (const layout of manifest?.layouts ?? []) {
    if (layout.entityKind) kinds.add(layout.entityKind);
    for (const section of layout.sections ?? []) {
      if (section.source?.entityKind) kinds.add(section.source.entityKind);
      if (section.source?.entityFilter?.kind) kinds.add(section.source.entityFilter.kind);
      for (const name of Object.keys(section.source?.fieldFilters ?? {})) fields.add(name);
      for (const option of section.filters ?? []) if (option.field) fields.add(option.field);
    }
  }
  const sorted = (set: Set<string>) =>
    [...set].filter((entry) => entry.trim()).sort((a, b) => a.localeCompare(b));
  return { entityKinds: sorted(kinds), pluginFields: sorted(fields) };
}

/** The rule's value in the wire form, or undefined when it is not usable yet. */
function wireValue(rule: SmartRule): unknown {
  const text = rule.value.trim();
  switch (smartValueKind(rule.field, rule.op)) {
    case "none":
      return null;
    case "days": {
      const days = Number(text);
      return Number.isInteger(days) && days >= 1 ? days : undefined;
    }
    case "date":
      return /^\d{4}-\d{2}-\d{2}$/.test(text) ? text : undefined;
    case "number": {
      if (!text) return undefined;
      const number = Number(text);
      if (!Number.isFinite(number)) return undefined;
      // Duration is entered in minutes and stored in seconds.
      return rule.field === "duration" ? Math.round(number * 60) : number;
    }
    default:
      if (!text) return undefined;
      // A plugin field compares as a number when the value is one.
      if (smartFieldClass(rule.field) === "plugin" && (rule.op === "is" || rule.op === "isNot")) {
        const number = Number(text);
        if (Number.isFinite(number)) return number;
      }
      return text;
  }
}

/** True when every rule names a known field and operator and has a usable value. */
export function smartRulesComplete(set: SmartRuleSet): boolean {
  if (set.rules.length > SMART_RULE_MAX_RULES) return false;
  if (set.limit !== null && !(set.limit >= 1 && set.limit <= SMART_RULE_MAX_LIMIT)) return false;
  return set.rules.every(
    (rule) => smartOperators(rule.field).includes(rule.op) && wireValue(rule) !== undefined,
  );
}

export function serializeSmartRules(set: SmartRuleSet): string {
  const rules = set.rules.map((rule) => {
    const value = wireValue(rule);
    return value === null || value === undefined
      ? { field: rule.field, op: rule.op }
      : { field: rule.field, op: rule.op, value };
  });
  const body: Record<string, unknown> = { kind: "rules", match: set.match, rules };
  if (set.limit !== null) body.limit = set.limit;
  return JSON.stringify(body);
}

export function serializeSmartSort(sort: SmartSort): string {
  return JSON.stringify({ by: sort.by, dir: sort.dir });
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function parseJSON(expr: string): Record<string, unknown> | null {
  try {
    return asRecord(JSON.parse(expr));
  } catch {
    return null;
  }
}

/** The rule set of a rule-source expression, or null for any other Smart Collection. */
export function parseSmartRules(expr: string): SmartRuleSet | null {
  const raw = parseJSON(expr);
  if (!raw || raw.kind !== "rules") return null;
  const rules: SmartRule[] = [];
  for (const entry of Array.isArray(raw.rules) ? raw.rules : []) {
    const rule = asRecord(entry);
    if (!rule) continue;
    const field = typeof rule.field === "string" ? rule.field : "";
    const op = typeof rule.op === "string" ? rule.op : "";
    let value = "";
    if (typeof rule.value === "number") {
      value = String(field === "duration" ? rule.value / 60 : rule.value);
    } else if (typeof rule.value === "string") {
      value = rule.value;
    }
    rules.push({ field, op, value });
  }
  const limit = typeof raw.limit === "number" && raw.limit >= 1 ? raw.limit : null;
  return { match: raw.match === "any" ? "any" : "all", rules, limit };
}

export function parseSmartSort(expr: string): SmartSort {
  const raw = parseJSON(expr);
  const by = typeof raw?.by === "string" && raw.by ? raw.by : "title";
  return { by, dir: raw?.dir === "desc" ? "desc" : "asc" };
}

function toSmartPlaylist(collection: SmartCollectionInfo): SmartPlaylist | null {
  const rules = parseSmartRules(collection.sourceExpr);
  if (!rules) return null;
  return {
    id: collection.id,
    libraryID: collection.libraryID,
    name: collection.name,
    rules,
    sort: parseSmartSort(collection.sortExpr),
  };
}

/** Every rule-based Smart Collection across the given Libraries, Library by Library. */
export async function loadSmartPlaylists(libraryIDs: readonly string[]): Promise<SmartPlaylist[]> {
  const lists = await Promise.all(
    libraryIDs.map((id) => fetchSmartCollections(id).catch(() => [] as SmartCollectionInfo[])),
  );
  return lists.flat().flatMap((collection) => toSmartPlaylist(collection) ?? []);
}

export interface SmartPlaylistDraft {
  libraryID: string;
  name: string;
  rules: SmartRuleSet;
  sort: SmartSort;
}

export async function saveSmartPlaylist(
  draft: SmartPlaylistDraft,
  id?: string,
): Promise<SmartPlaylist | null> {
  const sourceExpr = serializeSmartRules(draft.rules);
  const sortExpr = serializeSmartSort(draft.sort);
  const name = draft.name.trim();
  const saved = id
    ? await updateSmartCollection({ id, name, sourceExpr, sortExpr })
    : await createSmartCollection({ libraryID: draft.libraryID, name, sourceExpr, sortExpr });
  return toSmartPlaylist(saved);
}

/** Removes the smart playlist's rules; no Media is touched. */
export function deleteSmartPlaylist(id: string): Promise<SmartCollectionInfo> {
  return deleteSmartCollection(id);
}

/** The playlist's current items for the calling Profile, read in pages up to the read cap. */
export async function loadSmartPlaylistItems(
  playlist: SmartPlaylist,
  signal?: AbortSignal,
): Promise<SourceItem[]> {
  const cap = Math.min(playlist.rules.limit ?? SMART_RULE_MAX_LIMIT, SMART_PLAYLIST_READ_CAP);
  const items: SourceItem[] = [];
  while (items.length < cap) {
    const want = Math.min(PAGE_SIZE, cap - items.length);
    const page = await fetchSmartCollectionItems(playlist.id, want, items.length, signal);
    items.push(...page);
    if (page.length < want) break;
  }
  return items;
}
