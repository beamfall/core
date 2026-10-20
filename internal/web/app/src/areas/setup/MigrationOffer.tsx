import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { displayString } from "@/lib/display";
import { fetchImporters } from "@/lib/graphql";
import type { ImporterInfo } from "@/lib/graphql/types";
import { useI18n } from "@/lib/i18n";

// Seam A of the onboarding funnel (docs/design/onboarding-funnel.md §3): the
// inline migration offer presented as the final step of first-run setup.
//
// This is a BOUNDARY, not the importer: it lists what the active plugins
// declare and hands off to the existing importer surface (/console/remote,
// ADR-0037). It adds no import semantics. Skip is first-class (§3.2) — skipping
// finishes setup without running anything, and the post-setup checklist (Seam
// B) still pins the "Migrate from another server" item, so the path stays
// findable. Source labels come from each importer's plugin-declared display
// name; Core never hardcodes a source name (ADR-0037 §2.1 / invariant #1).
//
// Per invariant #8 the embedded web Console is NOT an App-Store binary, so this
// acquisition-adjacent surface is allowed here; framing stays generic ("bring
// your library") and source names are whatever the plugin declares.

// The importer surface this seam deep-links to (console RemoteAccess section).
const IMPORTER_PATH = "console/remote";

export function MigrationOffer({
  onSkip,
}: {
  /** Finish setup without migrating (Seam A skip — stays findable in Seam B). */
  onSkip: () => void;
}) {
  const { t, locale } = useI18n();
  const [importers, setImporters] = useState<ImporterInfo[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setImporters(null);
    setFailed(false);
    void fetchImporters()
      .then((list) => {
        if (!cancelled) setImporters(list);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, [retry]);

  // Deep-link to the importer surface and complete setup in one navigation. We
  // assign the BASE_URL-relative path (matching the wizard's window.location
  // hand-off) so the freshly-minted MANAGER session re-bootstraps and lands the
  // operator on the importer.
  function goToImporter() {
    window.location.assign(`${import.meta.env.BASE_URL}${IMPORTER_PATH}`);
  }

  const loading = importers === null && !failed;

  return (
    <section>
      <h1 className="m-0 text-lg font-semibold">{t("setupMigrateTitle")}</h1>
      <p className="mb-4 mt-0.5 text-[13px] text-muted-foreground">{t("setupMigrateLead")}</p>

      {/* Additive-only trust copy (funnel §3.3) — communicates the ADR-0037
          data-layer guarantees so a mid-evaluation refugee believes them. */}
      <ul className="m-0 mb-4 flex list-none flex-col gap-1.5 rounded-lg border bg-muted/25 p-3 text-[12px] leading-snug text-muted-foreground">
        <li>{t("setupMigrateTrustAdditive")}</li>
        <li>{t("setupMigrateTrustScope")}</li>
        <li>{t("setupMigrateTrustNoConnection")}</li>
        <li>{t("setupMigrateTrustRerunnable")}</li>
        <li>{t("setupMigrateTrustReportFirst")}</li>
      </ul>

      {loading ? (
        <Skeleton className="h-24 w-full" />
      ) : failed ? (
        // The offer still renders; only the source list is unavailable, so the
        // operator can still skip or open the importer.
        <div className="flex flex-col items-start gap-2">
          <p className="m-0 text-[13px] text-muted-foreground">
            {t("setupMigrateSourcesUnavailable")}
          </p>
          <Button type="button" variant="outline" size="sm" onClick={() => setRetry((n) => n + 1)}>
            {t("jobRetryAction")}
          </Button>
        </div>
      ) : importers && importers.length > 0 ? (
        <div>
          <p className="mb-1.5 text-xs text-muted-foreground">{t("setupMigrateSourcesLabel")}</p>
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {importers.map((imp) => (
              <li
                key={`${imp.pluginID}:${imp.id}`}
                className="rounded-lg border px-3 py-2.5 text-[13px]"
              >
                {displayString(imp.displayName, locale)}
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="text-[13px] text-muted-foreground">{t("setupMigrateNoSources")}</p>
      )}

      <div className="mt-[18px] flex items-center gap-2">
        <Button type="button" variant="outline" onClick={onSkip}>
          {t("setupMigrateSkip")}
        </Button>
        {(failed || (importers && importers.length > 0)) && (
          <Button type="button" onClick={goToImporter}>
            {t("setupMigrateContinue")}
          </Button>
        )}
      </div>
    </section>
  );
}
