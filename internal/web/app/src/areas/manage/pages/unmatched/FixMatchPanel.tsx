import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { fixMatch, fixMatchCandidates, fixMatchPreview, identifyLookup } from "@/lib/graphql";
import type {
  FixMatchCandidateInfo,
  FixMatchFieldDiff,
  UnmatchedItemInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { percent, reasonLabel } from "./reasons";

/**
 * FixMatchPanel is METAPARITY-4's full contract (ADR-0068 §A) over one queued
 * Media: candidates carrying confidence + source + why, a paste-a-provider-id
 * direct fetch, and a preview diff the operator sees BEFORE anything is written.
 * Realizes the approved `adr-safety-states` mockup.
 */
export function FixMatchPanel({
  item,
  onApplied,
  onError,
}: {
  item: UnmatchedItemInfo;
  onApplied: () => void;
  onError: (message: string) => void;
}) {
  const t = useT();
  const [query, setQuery] = useState(item.title || item.candidateTitle || "");
  const [candidates, setCandidates] = useState<FixMatchCandidateInfo[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [pastedID, setPastedID] = useState("");
  const [diff, setDiff] = useState<FixMatchFieldDiff[]>([]);
  const [busy, setBusy] = useState(false);

  const source = { pluginID: item.pluginID, scraperID: item.scraperID };

  // A new selection invalidates the preview: showing last candidate's diff next
  // to a different candidate's Apply button would misreport what is about to be
  // written.
  const select = (candidateID: string) => {
    setSelectedID(candidateID);
    setDiff([]);
  };

  const onSearch = async () => {
    const text = query.trim();
    if (!text) return;
    setBusy(true);
    try {
      const scored = await fixMatchCandidates({ mediaID: item.mediaID, ...source, query: text });
      setCandidates(scored);
      select(scored[0]?.id ?? "");
    } catch {
      onError(t("unmatchedUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  // Paste-an-id: fetch that one entry directly and present it as the single
  // candidate, so it flows through the same preview-then-apply path as a
  // searched one. It carries no score — nothing was compared.
  const onFetchByID = async () => {
    const candidateID = pastedID.trim();
    if (!candidateID) return;
    setBusy(true);
    try {
      const fetched = await identifyLookup({ mediaID: item.mediaID, ...source, candidateID });
      setCandidates([
        {
          ...fetched,
          ...source,
          confidence: 0,
          threshold: item.threshold,
          scored: false,
          reason: "unscored",
        },
      ]);
      select(fetched.id || candidateID);
    } catch {
      onError(t("unmatchedPasteIDFailed"));
    } finally {
      setBusy(false);
    }
  };

  const onPreview = async () => {
    if (!selectedID) return;
    setBusy(true);
    try {
      setDiff(await fixMatchPreview({ mediaID: item.mediaID, ...source, candidateID: selectedID }));
    } catch {
      onError(t("unmatchedUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  const onApply = async () => {
    if (!selectedID) return;
    setBusy(true);
    try {
      await fixMatch({ mediaID: item.mediaID, ...source, candidateID: selectedID });
      onApplied();
    } catch {
      onError(t("unmatchedUnavailable"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="rounded-md border border-border bg-card">
      <div className="border-b px-3 py-2">
        <h3 className="text-sm font-semibold text-foreground">{t("unmatchedCandidates")}</h3>
        <p className="text-xs text-muted-foreground">{t("unmatchedSearchByTitleYear")}</p>
      </div>
      <div className="space-y-3 p-3">
        <div className="flex gap-2">
          <Input
            aria-label={t("unmatchedSearchLabel")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <Button
            size="sm"
            variant="outline"
            disabled={busy || !query.trim()}
            onClick={() => void onSearch()}
          >
            {t("unmatchedSearch")}
          </Button>
        </div>

        <div className="grid gap-1">
          <p className="text-xs text-muted-foreground">{t("unmatchedPasteIDHint")}</p>
          <div className="flex gap-2">
            <Input
              aria-label={t("unmatchedPasteIDLabel")}
              value={pastedID}
              onChange={(e) => setPastedID(e.target.value)}
            />
            <Button
              size="sm"
              variant="outline"
              disabled={busy || !pastedID.trim()}
              onClick={() => void onFetchByID()}
            >
              {t("unmatchedPasteIDFetch")}
            </Button>
          </div>
        </div>

        {candidates.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            {busy ? t("loading") : t("unmatchedNoCandidates")}
          </p>
        ) : (
          <ul className="space-y-2">
            {candidates.map((candidate) => (
              <li key={candidate.id}>
                <button
                  type="button"
                  className="grid w-full gap-1 rounded-md border px-3 py-2 text-left hover:bg-muted/50 data-[active=true]:border-primary"
                  data-active={selectedID === candidate.id}
                  onClick={() => select(candidate.id)}
                >
                  <div className="flex items-start justify-between gap-2">
                    <span className="min-w-0 break-words text-sm font-medium text-foreground">
                      {candidate.title}
                      {candidate.year ? ` (${candidate.year})` : ""}
                    </span>
                    <Badge variant={candidate.scored ? "secondary" : "outline"}>
                      {candidate.scored
                        ? percent(candidate.confidence)
                        : t("unmatchedCandidateUnscored")}
                    </Badge>
                  </div>
                  <span className="text-xs text-muted-foreground">
                    {t("unmatchedCandidateSource", {
                      source: `${candidate.pluginID}/${candidate.scraperID}`,
                    })}
                    {candidate.scored
                      ? ` · ${t("unmatchedCandidateConfidence", {
                          confidence: percent(candidate.confidence),
                          threshold: percent(candidate.threshold),
                        })}`
                      : ""}
                  </span>
                  {candidate.reason && (
                    <span className="text-xs text-muted-foreground">
                      {reasonLabel(t, candidate.reason)}
                    </span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}

        <div className="grid gap-2 border-t pt-3">
          <div className="flex items-center justify-between gap-2">
            <h4 className="text-sm font-semibold text-foreground">
              {t("unmatchedPreviewHeading")}
            </h4>
            <Button
              size="sm"
              variant="outline"
              disabled={busy || !selectedID}
              onClick={() => void onPreview()}
            >
              {t("unmatchedPreviewLoad")}
            </Button>
          </div>
          <p className="text-xs text-muted-foreground">{t("unmatchedPreviewHint")}</p>
          {diff.length > 0 && (
            <dl className="grid gap-2">
              {diff.map((row) => (
                <div key={row.field} className="grid gap-0.5">
                  <dt className="text-xs text-muted-foreground">{row.field}</dt>
                  <dd className="text-sm text-foreground">
                    {row.changed ? (
                      <>
                        <span className="text-muted-foreground line-through">
                          {row.current || "—"}
                        </span>{" "}
                        → <span className="font-medium">{row.proposed}</span>
                      </>
                    ) : (
                      <span className="text-muted-foreground">
                        {t("unmatchedPreviewUnchanged")}
                      </span>
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          )}
          <Button size="sm" disabled={busy || !selectedID} onClick={() => void onApply()}>
            {t("unmatchedApplyPin")}
          </Button>
        </div>
      </div>
    </section>
  );
}
