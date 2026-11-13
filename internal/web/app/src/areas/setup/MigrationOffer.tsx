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
// declare and hands off to the existing importer surface (/console/migrate,
// ADR-0037). It adds no import semantics. Skip is first-class (§3.2) — skipping
// finishes setup without running anything, and the post-setup checklist (Seam
// B) still pins the "Migrate from another server" item, so the path stays
// findable. Source labels come from each importer's plugin-declared display
// name; Core never hardcodes a source name (ADR-0037 §2.1 / invariant #1).
//
// Per invariant #8 the embedded web Console is NOT an App-Store binary, so this
// acquisition-adjacent surface is allowed here; framing stays generic ("bring
// your library") and source names are whatever the plugin declares.

// The importer surface this seam deep-links to (the standalone Console Maintain › Migration route).
const IMPORTER_PATH = "console/migrate";

export function MigrationOffer({
  onSkip,
  embedded = false,
}: {
  /**
   * Finish setup without migrating (Seam A skip — stays findable in Seam B).
   * Omit it when the host already has a primary action that finishes setup.
   */
  onSkip?: () => void;
  /** Rendered inside another step: an h2 under that step's h1, set off by a rule. */
  embedded?: boolean;
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
  const Heading = embedded ? "h2" : "h1";

  return (
    <section className={embedded ? "mt-6 border-t border-white/10 pt-5" : undefined}>
      <Heading className={embedded ? "m-0 text-[15px] font-semibold" : "m-0 text-xl font-semibold"}>
        {t("setupMigrateTitle")}
      </Heading>
      <p className="mb-3 mt-1 text-dense leading-snug text-muted-foreground">
        {t("setupMigrateLead")}
      </p>

      {/* Additive-only trust copy (funnel §3.3) — communicates the ADR-0037
          data-layer guarantees so a mid-evaluation refugee believes them. */}
      <ul className="m-0 mb-4 flex list-disc flex-col gap-1 pl-5 text-dense leading-snug text-muted-foreground marker:text-muted-foreground/60">
        <li>{t("setupMigrateTrustAdditive")}</li>
        <li>{t("setupMigrateTrustScope")}</li>
        <li>{t("setupMigrateTrustNoConnection")}</li>
        <li>{t("setupMigrateTrustRerunnable")}</li>
        <li>{t("setupMigrateTrustReportFirst")}</li>
      </ul>

      {loading ? (
        <Skeleton className="h-10 w-full" />
      ) : failed ? (
        // The offer still renders; only the source list is unavailable, so the
        // operator can still skip or open the importer.
        <div className="flex flex-col items-start gap-2">
          <p className="m-0 text-dense text-muted-foreground">
            {t("setupMigrateSourcesUnavailable")}
          </p>
          <Button type="button" variant="outline" size="sm" onClick={() => setRetry((n) => n + 1)}>
            {t("jobRetryAction")}
          </Button>
        </div>
      ) : importers && importers.length > 0 ? (
        <div>
          <p className="m-0 mb-1 text-dense font-medium text-foreground/80">
            {t("setupMigrateSourcesLabel")}
          </p>
          <ul className="m-0 flex list-none flex-wrap gap-x-4 gap-y-1 p-0 text-sm">
            {importers.map((imp) => (
              <li key={`${imp.pluginID}:${imp.id}`}>{displayString(imp.displayName, locale)}</li>
            ))}
          </ul>
        </div>
      ) : (
        <p className="text-dense text-muted-foreground">{t("setupMigrateNoSources")}</p>
      )}

      {(onSkip || failed || (importers && importers.length > 0)) && (
        <div className="mt-4 flex flex-wrap items-center gap-2">
          {onSkip && (
            <Button type="button" variant="outline" onClick={onSkip}>
              {t("setupMigrateSkip")}
            </Button>
          )}
          {(failed || (importers && importers.length > 0)) && (
            <Button type="button" variant={onSkip ? "default" : "outline"} onClick={goToImporter}>
              {t("setupMigrateContinue")}
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
