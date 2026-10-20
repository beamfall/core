import { describe, expect, it } from "vitest";
import type { ImporterInfo, JobInfo } from "./graphql/types";
import {
  canRunImport,
  categoriesArgument,
  initialWizardConfig,
  isTerminalStatus,
  matchPercent,
  parseCategoryBreakdown,
  parseImportReport,
  toggleCategory,
  type WizardConfig,
} from "./migrationReport";

function importer(overrides: Partial<ImporterInfo> = {}): ImporterInfo {
  return {
    pluginID: "filmtv",
    id: "plex",
    displayName: { en: "Plex" },
    categories: ["watch-state", "ratings"],
    input: "export-file",
    conflict: "additive",
    credentialRef: "",
    credentialRequired: false,
    credentialConfigured: false,
    credentialStatus: "not-required",
    ...overrides,
  };
}

function job(progress: Partial<JobInfo["progress"]>, status = "done"): JobInfo {
  return {
    id: "job-1",
    kind: "migration",
    status,
    progress: { current: 0, total: 0, unit: "items", ...progress },
  };
}

describe("initialWizardConfig", () => {
  it("pre-selects every category and clears inputs", () => {
    const cfg = initialWizardConfig(importer());
    expect(cfg.selectedCategories).toEqual(["watch-state", "ratings"]);
    expect(cfg.sourcePath).toBe("");
    expect(cfg.profileID).toBe("");
    expect(cfg.libraryID).toBe("");
  });

  it("copies the category list (re-run reset is independent of the importer)", () => {
    const imp = importer();
    const cfg = initialWizardConfig(imp);
    cfg.selectedCategories.pop();
    expect(imp.categories).toEqual(["watch-state", "ratings"]); // unmutated
  });
});

describe("toggleCategory", () => {
  it("removes a selected category and re-adds a deselected one", () => {
    const after = toggleCategory(["watch-state", "ratings"], "ratings");
    expect(after).toEqual(["watch-state"]);
    expect(toggleCategory(after, "ratings")).toEqual(["watch-state", "ratings"]);
  });
});

describe("canRunImport — source path gating", () => {
  const base: WizardConfig = {
    sourcePath: "",
    selectedCategories: ["watch-state"],
    profileID: "",
    libraryID: "",
  };

  it("blocks an export-file importer until a non-blank path is given", () => {
    expect(canRunImport(importer(), base)).toBe(false);
    expect(canRunImport(importer(), { ...base, sourcePath: "   " })).toBe(false);
    expect(canRunImport(importer(), { ...base, sourcePath: "/exports/plex.db" })).toBe(true);
  });

  it("does not require a path for a credentials importer", () => {
    const imp = importer({
      input: "credentials",
      credentialRef: "emby-token",
      credentialRequired: true,
      credentialConfigured: true,
    });
    expect(canRunImport(imp, base)).toBe(true);
  });
});

describe("canRunImport — category gating", () => {
  it("blocks when no category is selected", () => {
    expect(
      canRunImport(importer(), {
        sourcePath: "/x",
        selectedCategories: [],
        profileID: "",
        libraryID: "",
      }),
    ).toBe(false);
  });
});

describe("canRunImport — credential gating", () => {
  const cfg: WizardConfig = {
    sourcePath: "",
    selectedCategories: ["watch-state"],
    profileID: "",
    libraryID: "",
  };

  it("blocks a credentials importer until its secret is configured", () => {
    const unconfigured = importer({
      input: "credentials",
      credentialRef: "emby-token",
      credentialRequired: true,
      credentialConfigured: false,
    });
    expect(canRunImport(unconfigured, cfg)).toBe(false);
    expect(canRunImport({ ...unconfigured, credentialConfigured: true }, cfg)).toBe(true);
  });
});

describe("categoriesArgument", () => {
  it("omits the argument when every category is selected", () => {
    expect(categoriesArgument(importer(), ["watch-state", "ratings"])).toBeUndefined();
  });
  it("sends the explicit subset when some are deselected", () => {
    expect(categoriesArgument(importer(), ["watch-state"])).toEqual(["watch-state"]);
  });
});

describe("parseCategoryBreakdown", () => {
  it("parses a multi-category breakdown suffix", () => {
    expect(parseCategoryBreakdown("12 of 15 items matched (watch-state=10, ratings=2)")).toEqual([
      { category: "watch-state", count: 10 },
      { category: "ratings", count: 2 },
    ]);
  });
  it("returns [] when there is no breakdown suffix", () => {
    expect(parseCategoryBreakdown("12 of 15 items matched")).toEqual([]);
  });
  it("skips malformed pairs without throwing", () => {
    expect(parseCategoryBreakdown("x (watch-state=, ratings=3, =5, bad)")).toEqual([
      { category: "ratings", count: 3 },
    ]);
  });
});

describe("parseImportReport", () => {
  it("derives matched/unmatched/rate and the per-category breakdown from progress", () => {
    const report = parseImportReport(
      job({ current: 12, total: 15, message: "12 of 15 items matched (watch-state=12)" }),
    );
    expect(report.matched).toBe(12);
    expect(report.unmatched).toBe(3);
    expect(report.total).toBe(15);
    expect(report.rate).toBeCloseTo(0.8, 5);
    expect(matchPercent(report)).toBe(80);
    expect(report.hasUnmatched).toBe(true);
    expect(report.perCategory).toEqual([{ category: "watch-state", count: 12 }]);
  });

  it("reports a full match as no-unmatched", () => {
    const report = parseImportReport(
      job({ current: 15, total: 15, message: "15 of 15 items matched" }),
    );
    expect(report.hasUnmatched).toBe(false);
    expect(matchPercent(report)).toBe(100);
  });

  it("yields an all-zero report for a job with no progress", () => {
    const report = parseImportReport(null);
    expect(report).toMatchObject({
      matched: 0,
      total: 0,
      unmatched: 0,
      rate: 0,
      hasUnmatched: false,
    });
    expect(report.perCategory).toEqual([]);
  });
});

describe("isTerminalStatus", () => {
  it("treats pending/active as non-terminal and everything else as terminal", () => {
    expect(isTerminalStatus("pending")).toBe(false);
    expect(isTerminalStatus("active")).toBe(false);
    expect(isTerminalStatus("done")).toBe(true);
    expect(isTerminalStatus("failed")).toBe(true);
  });
});
