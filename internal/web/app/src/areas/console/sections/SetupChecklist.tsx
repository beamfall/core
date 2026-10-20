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
// declaring the funnel finished. Reading this state leaks no secret — only
// whether each step finished and whether any content is browsable.

// Each spine item: a setup-step boolean derived from SetupState, a deep-link to
// the Console/app station that completes it, and its label. The destinations
// are the live routes: Libraries (scan), RemoteAccess (migrate, /console/remote),
// the browser-promotion page (enroll), and Profiles & Access (profiles).
interface SpineItem {
  label: StringKey;
  to: string;
  done: (s: SetupState) => boolean;
}

const SPINE: SpineItem[] = [
  {
    label: "setupChecklistScan",
    to: "/console/libraries",
    done: (s) => s.hasLibrary,
  },
  {
    label: "setupChecklistMigrate",
    to: "/console/remote",
    // Migration is optional and has no SetupState flag; it is always offered as
    // a forward step (the operator skipped it at Seam A, or never had a source).
    done: () => false,
  },
  {
    label: "setupChecklistEnroll",
    to: "/promote",
    // Enrollment has no SetupState flag either; always an available next step
    // (this is where the operator learns pairing exists — funnel §5).
    done: () => false,
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
async function fetchHasBrowsableContent(signal?: AbortSignal): Promise<boolean> {
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

  // "Done = first successful browse." Setup-green with content → finished;
  // setup-green but no browsable content (or the probe is still loading /
  // failed) → keep guiding toward first value (scan / migrate).
  function footerMessage(): string {
    if (!data?.complete) return t("setupIncomplete");
    if (browse.data === true) return t("setupChecklistFirstValue");
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
            void refresh();
            void browse.refresh();
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
            {SPINE.map((item, i) => {
              const done = item.done(data);
              return (
                <Link
                  key={item.label}
                  to={item.to}
                  className={`console-step${done ? " is-done" : ""}`}
                >
                  <span className="console-step-index" aria-hidden>
                    {done ? <Check className="h-3.5 w-3.5" /> : i + 1}
                  </span>
                  <span className="console-step-label">{t(item.label)}</span>
                  <StatusChip tone={done ? "green" : "amber"}>
                    {t(done ? "setupChecklistDone" : "setupChecklistNext")}
                  </StatusChip>
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
