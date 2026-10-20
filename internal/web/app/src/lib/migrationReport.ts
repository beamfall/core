// Pure helpers for the Migration Wizard (ADR-0037): the run-gating predicate,
// the unified match-rate report parser, and the configure-step reset. They are
// extracted from RemoteAccess.tsx so the wizard's actual behavior — when Run is
// allowed, what the report says, and what a re-run resets — is unit-testable
// without rendering React.

import type { ImporterInfo, JobInfo } from "./graphql/types";

/** The configurable inputs of one wizard run. */
export interface WizardConfig {
  /** Operator-supplied export-file path / connection string. */
  sourcePath: string;
  /** Selected data categories (subset of importer.categories). */
  selectedCategories: string[];
  /** Target Profile id ("" = the operator's own). */
  profileID: string;
  /** Target Library id ("" = match across every Library). */
  libraryID: string;
}

/** The configure-step starting state for an importer: all categories selected,
 * empty source path, default Profile/Library (server-resolved). A re-run resets
 * to exactly this so the same importer is immediately runnable again (additive
 * semantics — ADR-0037 §2.4). */
export function initialWizardConfig(importer: ImporterInfo): WizardConfig {
  return {
    sourcePath: "",
    selectedCategories: [...importer.categories],
    profileID: "",
    libraryID: "",
  };
}

/** Toggle one category in/out of the selection, preserving the others. */
export function toggleCategory(selected: string[], category: string): string[] {
  return selected.includes(category)
    ? selected.filter((c) => c !== category)
    : [...selected, category];
}

/** Whether the run can start. A run requires at least one category; an
 * export-file importer additionally requires a non-blank source path; a
 * credentials importer additionally requires its secret to be configured.
 * Mirrors the server's own refusals (graph runImport) so the UI blocks early
 * rather than round-tripping a guaranteed error. */
export function canRunImport(importer: ImporterInfo, config: WizardConfig): boolean {
  if (config.selectedCategories.length === 0) return false;
  if (importer.input === "export-file" && config.sourcePath.trim() === "") {
    return false;
  }
  if (importer.credentialRequired && !importer.credentialConfigured) {
    return false;
  }
  return true;
}

/** The categories argument to send to runImport: omit it (undefined) when every
 * category is selected so the server uses the importer's full declared set;
 * otherwise send the explicit subset. */
export function categoriesArgument(
  importer: ImporterInfo,
  selected: string[],
): string[] | undefined {
  return selected.length < importer.categories.length ? selected : undefined;
}

/** A category=count pair parsed from the report's breakdown suffix. */
export interface CategoryCount {
  category: string;
  count: number;
}

/** The unified match-rate report (ADR-0037 §2.3): the overall matched/total
 * counts, the derived rate, the per-category breakdown, and whether any item
 * went unmatched (so the wizard can offer the Manage unmatched route). It is
 * derived from the job's progress row — current/total counts plus the message's
 * "(watch-state=12, ratings=3)" breakdown suffix the handler emits. */
export interface MatchReport {
  matched: number;
  total: number;
  unmatched: number;
  /** Match rate in [0,1]; 0 when total is 0. */
  rate: number;
  perCategory: CategoryCount[];
  hasUnmatched: boolean;
}

/** Parse a job's progress into the unified match-rate report. The counts come
 * from progress.current/total (authoritative); the per-category breakdown is
 * parsed from the parenthesized suffix of progress.message when present. A job
 * with no progress yet yields an all-zero report. */
export function parseImportReport(job: JobInfo | null): MatchReport {
  const matched = job?.progress.current ?? 0;
  const total = job?.progress.total ?? 0;
  const unmatched = Math.max(total - matched, 0);
  const rate = total > 0 ? matched / total : 0;
  return {
    matched,
    total,
    unmatched,
    rate,
    perCategory: parseCategoryBreakdown(job?.progress.message ?? ""),
    hasUnmatched: total > 0 && matched < total,
  };
}

/** Extract the "(watch-state=12, ratings=3)" breakdown from a report message.
 * Returns [] when the message has no breakdown suffix. Malformed pairs (no
 * number, negative) are skipped rather than throwing — the headline counts are
 * authoritative regardless. */
export function parseCategoryBreakdown(message: string): CategoryCount[] {
  const open = message.lastIndexOf("(");
  const close = message.lastIndexOf(")");
  if (open === -1 || close === -1 || close <= open) return [];
  const inner = message.slice(open + 1, close);
  const out: CategoryCount[] = [];
  for (const part of inner.split(",")) {
    const [rawCat, rawCount] = part.split("=");
    if (rawCount === undefined) continue;
    const category = rawCat.trim();
    const countText = rawCount.trim();
    if (countText === "") continue;
    const count = Number(countText);
    if (!category || !Number.isFinite(count) || count < 0) continue;
    out.push({ category, count });
  }
  return out;
}

/** A whole-number percent (0–100) for display, e.g. rate 0.965 → 97. */
export function matchPercent(report: MatchReport): number {
  return Math.round(report.rate * 100);
}

/** Whether the polled job has left the active states and the wizard should
 * stop polling. "done" → done phase; any other terminal status → error. */
export function isTerminalStatus(status: string): boolean {
  return status !== "pending" && status !== "active";
}
