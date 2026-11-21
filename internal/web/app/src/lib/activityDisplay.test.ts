import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { formatMessage } from "@/lib/i18n";
import { type Locale, STRINGS } from "@/strings";
import {
  ACTIVITY_CATEGORIES,
  ACTIVITY_CATEGORY_LABELS,
  ACTIVITY_LABELS,
  type ActivityNames,
  activityMatches,
  describeActivity,
  formatLocalTime,
  formatRelativeTime,
  formatUtcTime,
  humanizeActivityKey,
  parseActivityTime,
  type Translate,
} from "./activityDisplay";

// The same lookup useT performs, bound to one locale's resolved table.
function translator(locale: Locale): Translate {
  return (key, values) => formatMessage(STRINGS[locale][key], values, locale);
}

const t = translator("en");

// The server's closed action vocabulary, read from its Go source (vitest runs
// from internal/web/app).
function serverActions(): string[] {
  const source = readFileSync(resolve(process.cwd(), "../../store/activity.go"), "utf8");
  return [...source.matchAll(/^\s*Activity\w+\s*=\s*"([^"]+)"/gm)].map((match) => match[1]);
}

const names: ActivityNames = {
  profiles: new Map([
    ["p-alex", "Alex"],
    ["p-sam", "Sam"],
  ]),
  devices: new Map([["d-tv", "Living Room TV"]]),
  libraries: new Map([["lib-films", "Films"]]),
  media: new Map([["m-film", "Night Train"]]),
};

const base = { id: "1", occurredAt: "2026-11-12T03:23:23Z" };

describe("activity labels", () => {
  it("has a sentence for every action the server can record", () => {
    const actions = serverActions();
    // Guards the parse itself: an empty list would pass the check below.
    expect(actions.length).toBeGreaterThan(80);
    expect(actions).toContain("media.trash");
    const unlabelled = actions.filter((action) => !(action in ACTIVITY_LABELS));
    expect(unlabelled, "add these to ACTIVITY_LABELS in activityDisplay.ts").toEqual([]);
  });

  it("labels no action the server does not record", () => {
    const actions = new Set(serverActions());
    expect(Object.keys(ACTIVITY_LABELS).filter((action) => !actions.has(action))).toEqual([]);
  });

  it("files every sentence under a category that has a name", () => {
    for (const [action, entry] of Object.entries(ACTIVITY_LABELS)) {
      expect(ACTIVITY_CATEGORIES, action).toContain(entry.category);
      expect(ACTIVITY_CATEGORY_LABELS[entry.category], action).toBeTruthy();
    }
  });

  it("names every sentence and category in the localized catalog", () => {
    for (const [action, entry] of Object.entries(ACTIVITY_LABELS)) {
      expect(STRINGS.en[entry.sentence], action).toBeTruthy();
      if (entry.noun) expect(STRINGS.en[entry.noun], action).toBeTruthy();
    }
  });

  it("leaves no placeholder behind in any locale, with or without names", () => {
    for (const locale of Object.keys(STRINGS) as Locale[]) {
      const tl = translator(locale);
      for (const action of Object.keys(ACTIVITY_LABELS)) {
        for (const entry of [
          { ...base, action },
          { ...base, action, profileID: "p-alex", targetType: "media", targetID: "m-1" },
          { ...base, action, tokenID: "tok-1", targetType: "media", targetID: "m-film" },
        ]) {
          const { sentence, categoryLabel } = describeActivity(entry, tl, names);
          expect(sentence, `${locale} ${action}`).not.toMatch(/[{}]/);
          expect(sentence, `${locale} ${action}`).toMatch(/^\p{Lu}/u);
          expect(categoryLabel, `${locale} ${action}`).toBeTruthy();
        }
      }
    }
  });
});

describe("describeActivity", () => {
  it("says who did what", () => {
    const row = describeActivity(
      { ...base, action: "media.trash", profileID: "p-alex", targetType: "media", targetID: "m-1" },
      t,
      names,
    );
    expect(row).toMatchObject({
      sentence: "Alex moved an item to Trash",
      category: "library",
      categoryLabel: "Library & media",
      known: true,
    });
  });

  it("names the library scan when the server made the change", () => {
    const merge = { ...base, action: "media.merge", targetType: "media", targetID: "m-1" };
    expect(describeActivity({ ...merge, systemActor: "scan" }, t, names).sentence).toBe(
      "The library scan merged items",
    );
    expect(describeActivity(merge, t, names).sentence).toBe("Someone merged items");
  });

  it("names a media item whose title the viewer may see", () => {
    const media = { ...base, profileID: "p-alex", targetType: "media" };
    expect(
      describeActivity({ ...media, action: "media.trash", targetID: "m-film" }, t, names).sentence,
    ).toBe("Alex moved Night Train to Trash");
    // A hidden item has no title in the names, so it keeps the generic noun.
    expect(
      describeActivity({ ...media, action: "media.restore", targetID: "m-hidden" }, t, names)
        .sentence,
    ).toBe("Alex restored an item from Trash");
    // Only a Media target is looked up by Media id.
    expect(
      describeActivity(
        { ...media, action: "media.trash", targetType: "library", targetID: "m-film" },
        t,
        names,
      ).sentence,
    ).toBe("Alex moved an item to Trash");
  });

  it("writes the sentence in the viewer's language", () => {
    const entry = {
      ...base,
      action: "media.trash",
      profileID: "p-alex",
      targetType: "media",
      targetID: "m-film",
    };
    const fr = describeActivity(entry, translator("fr"), names);
    expect(fr.sentence).toContain("Alex");
    expect(fr.sentence).toContain("Night Train");
    expect(fr.sentence).not.toBe(describeActivity(entry, t, names).sentence);
    expect(fr.categoryLabel).toBe(STRINGS.fr.activityCategoryLibrary);
  });

  it("names the profile, device, library, plugin or settings area acted on", () => {
    const describe = (entry: object) =>
      describeActivity({ ...base, profileID: "p-alex", action: "", ...entry }, t, names).sentence;
    expect(describe({ action: "profile.owner_transfer", targetID: "p-sam" })).toBe(
      "Alex made Sam the owner",
    );
    expect(describe({ action: "device.revoke", targetID: "d-tv" })).toBe(
      "Alex revoked Living Room TV",
    );
    expect(describe({ action: "library.delete", targetID: "lib-films" })).toBe(
      "Alex deleted Films",
    );
    expect(describe({ action: "plugin.update", targetID: "tmdb" })).toBe(
      "Alex updated the tmdb plugin",
    );
    expect(describe({ action: "config.update", targetType: "transcode" })).toBe(
      "Alex changed transcode settings",
    );
    expect(describe({ action: "device.revoke", targetID: "d-gone" })).toBe("Alex revoked a device");
  });

  it("attributes a change made with an API credential", () => {
    expect(describeActivity({ ...base, action: "trash.purge", tokenID: "tok-1" }, t).sentence).toBe(
      "An API token permanently emptied Trash",
    );
    expect(describeActivity({ ...base, action: "trash.purge" }, t).sentence).toBe(
      "Someone permanently emptied Trash",
    );
  });

  it("falls back to a readable form of an unknown key", () => {
    const row = describeActivity({ ...base, action: "widget.frobnicate_all" }, t, names);
    expect(row).toMatchObject({
      sentence: "Widget frobnicate all",
      category: "other",
      known: false,
    });
    expect(humanizeActivityKey("saved_filter.delete")).toBe("Saved filter delete");
  });

  it("matches a search on the sentence, the raw key and names", () => {
    const entry = { ...base, action: "media.trash", profileID: "p-alex", libraryID: "lib-films" };
    const row = describeActivity(entry, t, names);
    for (const query of ["trash", "ALEX", "media.trash", "films", "  "]) {
      expect(activityMatches(entry, row, query), query).toBe(true);
    }
    expect(activityMatches(entry, row, "plugin")).toBe(false);
  });
});

describe("activity times", () => {
  const now = new Date("2026-11-12T12:00:00Z");
  const ago = (seconds: number) => new Date(now.getTime() - seconds * 1000);

  it("reads as a relative time in the viewer's locale", () => {
    expect(formatRelativeTime(ago(20), now, "en")).toBe("now");
    expect(formatRelativeTime(ago(5 * 60), now, "en")).toBe("5 minutes ago");
    expect(formatRelativeTime(ago(3 * 3600), now, "en")).toBe("3 hours ago");
    expect(formatRelativeTime(ago(86_400), now, "en")).toBe("yesterday");
    expect(formatRelativeTime(ago(5 * 60), now, "fr")).toBe("il y a 5 minutes");
  });

  it("shows the local time in the viewer's zone and the exact time in UTC", () => {
    const at = parseActivityTime("2026-11-12T03:23:23Z")!;
    expect(formatLocalTime(at, "en-US", "America/Los_Angeles")).toMatch(/Nov 11, 2026.*7:23/);
    expect(formatLocalTime(at, "en-GB", "Europe/Paris")).toMatch(/12 Nov 2026.*04:23/);
    expect(formatUtcTime(at)).toBe("2026-11-12 03:23:23 UTC");
    expect(parseActivityTime("not a time")).toBeNull();
  });
});
