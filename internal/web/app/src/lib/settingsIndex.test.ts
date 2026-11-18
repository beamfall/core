import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import type { StringKey } from "@/strings";
import { en } from "@/strings/catalog";
import {
  normalizeSearchText,
  SETTINGS_INDEX,
  type SearchablePage,
  searchSettings,
} from "./settingsIndex";

const AREAS = resolve(__dirname, "../areas");

function sources(dir: string): { file: string; text: string }[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return sources(path);
    if (!entry.name.endsWith(".tsx") || entry.name.endsWith(".test.tsx")) return [];
    return [{ file: path, text: readFileSync(path, "utf8") }];
  });
}

const t = (key: StringKey) => en[key] ?? key;

describe("settings index anchors", () => {
  const files = sources(AREAS);
  const literal = new Set<string>();
  // `setting={`console.playback.${field}`}` style anchors: the prefix, and the
  // file that writes it, whose quoted literals must include the suffix.
  const templates: { prefix: string; text: string }[] = [];
  for (const { text } of files) {
    for (const m of text.matchAll(/(?:data-setting|setting)="([^"]+)"/g)) literal.add(m[1]);
    for (const m of text.matchAll(/(?:data-setting|setting)=\{`([^`$]+)\$\{/g)) {
      templates.push({ prefix: m[1], text });
    }
  }

  const ids = SETTINGS_INDEX.flatMap((page) => page.settings.map((s) => s.id));

  it.each(ids)("finds the control for %s in a page", (id) => {
    if (literal.has(id)) return;
    const template = templates.find((tp) => id.startsWith(tp.prefix));
    expect(template, `no data-setting anchor for ${id}`).toBeDefined();
    const suffix = id.slice(template?.prefix.length ?? 0);
    // The suffix is a quoted value ("codec") or an object key (conversion:).
    const name = suffix.replace(/[-.]/g, "\\$&");
    expect(template?.text).toMatch(new RegExp(`["']${name}["']|\\b${name}:`));
  });

  it("gives every setting a unique id", () => {
    expect(new Set(ids).size).toBe(ids.length);
  });

  it("points every page at a route its area has", () => {
    const consoleIndex = readFileSync(join(AREAS, "console/index.tsx"), "utf8");
    const manageIndex = readFileSync(join(AREAS, "manage/index.tsx"), "utf8");
    for (const page of SETTINGS_INDEX) {
      if (page.area === "console") expect(consoleIndex).toContain(`path: "${page.path}"`);
      else expect(manageIndex).toContain(`to: "/manage/${page.path}"`);
    }
  });
});

describe("searchSettings", () => {
  const pages: SearchablePage[] = [
    { area: "console", path: "config", label: "navPlaybackSignIn" },
    { area: "manage", path: "trash", label: "navTrash", lead: "manageTrashLead" },
  ];

  it("returns nothing for an empty query", () => {
    expect(searchSettings("   ", pages, t)).toEqual([]);
  });

  it("finds a setting by its label and opens its page at the control", () => {
    const [first] = searchSettings("transcodes", pages, t);
    expect(first).toMatchObject({
      area: "console",
      path: "config",
      setting: "console.playback.maxTranscodes",
      page: "Playback & sign-in",
    });
  });

  it("finds a page by its name", () => {
    const results = searchSettings("trash", pages, t);
    expect(results.some((r) => r.key === "manage:trash" && !r.setting)).toBe(true);
  });

  it("needs every word and ignores case and accents", () => {
    expect(normalizeSearchText("Réglages AVANCÉS")).toBe("reglages avances");
    expect(searchSettings("TRANSCODES zzzz", pages, t)).toEqual([]);
  });

  it("ranks a label that starts with the query above a help-only match", () => {
    const results = searchSettings("pairing", pages, t, { limit: 50 });
    const labels = results.map((r) => normalizeSearchText(r.label));
    const firstHelpOnly = labels.findIndex((l) => !l.includes("pairing"));
    const lastStarts = labels.findLastIndex((l) => l.startsWith("pairing"));
    if (firstHelpOnly >= 0 && lastStarts >= 0) expect(lastStarts).toBeLessThan(firstHelpOnly);
    expect(results.length).toBeGreaterThan(0);
  });

  it("leaves App Store-firewalled Console pages out on an App Store host", () => {
    const all = searchSettings("conversion", [], t, { limit: 50 });
    expect(all.some((r) => r.path === "identification")).toBe(true);
    const store = searchSettings("conversion", [], t, { limit: 50, appStoreHost: true });
    expect(store.some((r) => r.path === "identification")).toBe(false);
  });

  it("marks Advanced settings so opening one turns Advanced on", () => {
    const [codec] = searchSettings("codec", [], t).filter(
      (r) => r.setting === "console.playback.codec",
    );
    expect(codec?.advanced).toBe(true);
  });
});
