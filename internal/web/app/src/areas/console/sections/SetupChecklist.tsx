import { Check, RefreshCw } from "lucide-react";
import { Link } from "react-router";
import { ErrorState, StatusChip } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { fetchJobs, fetchSetupState, fetchSourceItems, retryJob } from "@/lib/graphql";
import type { JobInfo, SetupState } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { jobProgressDetail } from "@/lib/jobDisplay";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Seam B of the onboarding funnel (docs/design/onboarding-funnel.md §5): the
// post-setup checklist is the funnel SPINE, not a dead-end list. Each item
// deep-links to its owning station and reports done / not-started, so the
// operator always knows the single next step toward first value.
//
// "Done" is NOT "setup green" — it is FIRST SUCCESSFUL BROWSE (funnel §2). So
// when every setup step is complete but the library still has no browsable
// content, the footer keeps pointing forward (scan / migrate) instead of
// declaring the funnel finished. The state carries only whether each step
// finished and whether any content is browsable.
//
// Migrating and adding a device are optional and have no completion flag, so
// they read "Optional", never "Next step": the list must not ask for more work
// underneath a footer that says the server is all set.

// Each spine item: a setup-step boolean derived from SetupState, a deep-link to
// the Console/app station that completes it, and its label. The destinations
// are the live routes: Libraries (scan), MigrationWizard (migrate, /console/migrate),
// the browser-promotion page (enroll), and Profiles & Access (profiles).
interface FirstValueProgress {
  /** At least one catalog item is browsable. */
  browsable: boolean;
  /**
   * The browsable content came from a finished route: a scan job completed
   * having brought files in (a completed scan's progress counts the files it
   * imported), or jobs loaded with no scan on record, so the content arrived
   * some other way (a restore or an import). An empty completed scan beside a
   * failed one that left items is neither.
   */
  contentLanded: boolean;
}

interface SpineItem {
  label: StringKey;
  to: string;
  done: (s: SetupState, progress: FirstValueProgress) => boolean;
  /** Never required for a first browse; shown as "Optional" until done. */
  optional?: boolean;
}

const SPINE: SpineItem[] = [
  {
    label: "setupChecklistScan",
    to: "/console/libraries",
    // A Library alone is not a scan (CORE-1606): Done only once a completed
    // scan brought files in and the catalog holds browsable content. A scan
    // that added some items and then failed or was canceled is not Done, even
    // beside an earlier completed scan that found nothing. Content that came
    // in with no scan on record is Done: the step is about getting content in.
    done: (s, p) => s.hasLibrary && p.browsable && p.contentLanded,
  },
  {
    label: "setupChecklistMigrate",
    to: "/console/migrate",
    // Migration is optional and has no SetupState flag; it is always offered as
    // a forward step (the operator skipped it at Seam A, or never had a source).
    done: () => false,
    optional: true,
  },
  {
    label: "setupChecklistEnroll",
    to: "/promote",
    // Enrollment has no SetupState flag either; always available
    // (this is where the operator learns pairing exists — funnel §5).
    done: () => false,
    optional: true,
  },
  {
    label: "setupChecklistProfiles",
    to: "/console/access",
    done: (s) => s.hasOwnerProfile,
  },
];

// The "complete-but-empty" probe: after setup is green, ask the catalog for a
// single browsable item. Zero items means no first value yet, so the footer
// must keep guiding forward. Respects Reveal/access exactly like normal browse.
export async function fetchHasBrowsableContent(signal?: AbortSignal): Promise<boolean> {
  const items = await fetchSourceItems(
    {
      kind: "",
      entityKind: "",
      libraryID: "",
      entityID: "",
      fieldFilters: null,
      entityFilter: null,
      limit: 1,
      offset: 0,
    },
    signal,
  );
  return items.length > 0;
}

function firstValueProgress(browsable: boolean | null, jobs: JobInfo[] | null): FirstValueProgress {
  const scans = (jobs ?? []).filter((job) => job.kind === "scan");
  const scanBroughtFiles = scans.some(
    (job) => job.status === "completed" && job.progress.current > 0,
  );
  return {
    browsable: browsable === true,
    // Jobs that failed to load are unknown, never "no scan on record".
    contentLanded: scanBroughtFiles || (jobs !== null && scans.length === 0),
  };
}

/** True while any required checklist step is still open (Overview puts it first then). */
export function firstValueOpen(
  state: SetupState | null,
  browsable: boolean | null,
  jobs: JobInfo[] | null,
): boolean {
  if (!state) return true;
  const progress = firstValueProgress(browsable, jobs);
  return SPINE.some((item) => !item.optional && !item.done(state, progress));
}

const dismissedMigrationResumeKey = "beamfall:onboarding:dismissed-migration-resume";

function migrationResumeJob(jobs: JobInfo[]): JobInfo | null {
  const dismissed = new Set(
    (globalThis.localStorage?.getItem(dismissedMigrationResumeKey) || "")
      .split(",")
      .filter(Boolean),
  );
  return (
    jobs.find(
      (job) =>
        job.kind === "migration" &&
        (job.status === "canceled" || job.status === "failed") &&
        !dismissed.has(job.id),
    ) ?? null
  );
}

export function SetupChecklist() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchSetupState);
  const jobs = useAsync(fetchJobs);
  // Only meaningful once setup is complete; a best-effort signal that degrades
  // to "show forward guidance" if it cannot load (never a false "all done").
  const browse = useAsync(fetchHasBrowsableContent);
  const resume = migrationResumeJob(jobs.data ?? []);
  const progress = firstValueProgress(browse.data, jobs.data);

  // "Done = first successful browse." Setup-green with content that landed
  // (see FirstValueProgress) → finished; otherwise (no browsable content, only
  // an unfinished scan behind it, or a probe still loading / failed) → keep
  // guiding toward first value.
  function footerMessage(): string {
    if (!data?.complete) return t("setupIncomplete");
    if (progress.browsable && progress.contentLanded) return t("setupChecklistFirstValue");
    return t("setupChecklistEmptyNextStep");
  }

  return (
    <Section
      heading="setupChecklistHeading"
      lead="setupChecklistLead"
      actions={
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            // Reload everything the steps and footer are derived from.
            void refresh();
            void browse.refresh();
            void jobs.refresh();
          }}
        >
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("refresh")}
        </Button>
      }
    >
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed || !data ? (
        <ErrorState message="setupChecklistUnavailable" />
      ) : (
        <div className="space-y-3">
          <div className="console-card">
            {SPINE.map((item) => {
              const done = item.done(data, progress);
              return (
                <Link
                  key={item.label}
                  to={item.to}
                  className={`console-step${done ? " is-done" : ""}`}
                >
                  <span className="console-step-index" aria-hidden>
                    {done && <Check className="h-3.5 w-3.5" />}
                  </span>
                  <span className="console-step-label">{t(item.label)}</span>
                  {done ? (
                    <StatusChip tone="green">{t("setupChecklistDone")}</StatusChip>
                  ) : item.optional ? (
                    <StatusChip tone="neutral">{t("setupChecklistOptional")}</StatusChip>
                  ) : (
                    <StatusChip tone="amber">{t("setupChecklistNext")}</StatusChip>
                  )}
                </Link>
              );
            })}
          </div>
          {resume && (
            <div className="console-card console-card-pad space-y-3">
              <div>
                <p className="m-0 text-sm font-semibold">{t("setupChecklistMigrationResume")}</p>
                <p className="m-0 mt-1 text-xs text-muted-foreground">
                  {jobProgressDetail(resume, t) || t("setupChecklistMigrationPartial")}
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  onClick={() => {
                    void retryJob(resume.id).then(() => void jobs.refresh());
                  }}
                >
                  {t("setupChecklistResume")}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    void retryJob(resume.id).then(() => void jobs.refresh());
                  }}
                >
                  {t("setupChecklistRerun")}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    const dismissed = new Set(
                      (localStorage.getItem(dismissedMigrationResumeKey) || "")
                        .split(",")
                        .filter(Boolean),
                    );
                    dismissed.add(resume.id);
                    localStorage.setItem(dismissedMigrationResumeKey, [...dismissed].join(","));
                    void jobs.refresh();
                  }}
                >
                  {t("setupChecklistDiscard")}
                </Button>
              </div>
            </div>
          )}
          <p className="text-sm font-medium">{footerMessage()}</p>
        </div>
      )}
    </Section>
  );
}
