import { useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { SuppressionScope, SuppressionTarget } from "@/lib/graphql";
import { resetUnmatchedSuppression, suppressUnmatched } from "@/lib/graphql";
import type { UnmatchedRecordInfo, UnmatchedSuppressionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

/**
 * The three ADR-0068 scopes in the order the approved `fix-match-suppression`
 * mockup presents them, source record first as the default. Scope is always
 * chosen explicitly — never inferred from a generic "Ignore".
 */
const SCOPES: { scope: SuppressionScope; label: StringKey; hint: StringKey }[] = [
  {
    scope: "source-record",
    label: "unmatchedSuppressScopeRecord",
    hint: "unmatchedSuppressScopeRecordHint",
  },
  {
    scope: "file",
    label: "unmatchedSuppressScopeFile",
    hint: "unmatchedSuppressScopeFileHint",
  },
  {
    scope: "candidate",
    label: "unmatchedSuppressScopeCandidate",
    hint: "unmatchedSuppressScopeCandidateHint",
  },
];

/**
 * suppressionTarget builds the identity one scope addresses. Suppress and reset
 * MUST address an identity identically or a reset could silently miss its own
 * suppression, so both callers route through this single builder.
 */
function suppressionTarget(
  scope: SuppressionScope,
  record: UnmatchedRecordInfo,
): SuppressionTarget {
  if (scope === "file") {
    return {
      scope,
      fingerprint: record.mediaID || record.id,
      sourceHandle: record.sourceHandle,
    };
  }
  if (scope === "candidate") {
    return { scope, recordID: record.id, candidateID: record.candidateID };
  }
  return { scope, recordID: record.id };
}

/**
 * SuppressionPanel is METAPARITY-7 (ADR-0068 §A): a named scope picker
 * defaulting to the source record, each active suppression showing its own
 * provenance, and each carrying its OWN reset so lifting one scope leaves both
 * siblings suppressed.
 */
export function SuppressionPanel({
  record,
  suppressions,
  onChanged,
}: {
  record: UnmatchedRecordInfo;
  suppressions: UnmatchedSuppressionInfo[];
  onChanged: () => void;
}) {
  const t = useT();
  const [scope, setScope] = useState<SuppressionScope>("source-record");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const run = async (action: () => Promise<unknown>) => {
    setBusy(true);
    setError("");
    try {
      await action();
      onChanged();
    } catch {
      // The mockup's `error` state: the previous scope stays authoritative and
      // the retry cannot create a partial suppression (the write is idempotent).
      setError(t("unmatchedSuppressFailed"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid gap-3 rounded-md border border-border bg-card p-3">
      <h4 className="text-sm font-semibold text-foreground">{t("unmatchedSuppressHeading")}</h4>
      <fieldset className="grid gap-2 border-0 p-0">
        <legend className="pb-1 text-xs font-semibold text-foreground">
          {t("unmatchedSuppressScopeLegend")}
        </legend>
        {SCOPES.map((option) => (
          <label key={option.scope} className="flex items-start gap-2 text-sm">
            <input
              type="radio"
              name="unmatched-suppression-scope"
              value={option.scope}
              checked={scope === option.scope}
              disabled={option.scope === "candidate" && !record.candidateID}
              onChange={() => setScope(option.scope)}
            />
            <span className="grid gap-0.5">
              <span className="font-medium text-foreground">{t(option.label)}</span>
              <span className="text-xs text-muted-foreground">{t(option.hint)}</span>
            </span>
            {option.scope === "source-record" && (
              <Badge variant="outline">{t("unmatchedSuppressRecommended")}</Badge>
            )}
          </label>
        ))}
      </fieldset>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <Button
        size="sm"
        variant="outline"
        disabled={busy}
        onClick={() => void run(() => suppressUnmatched(suppressionTarget(scope, record)))}
      >
        {t("unmatchedSuppressSubmit")}
      </Button>

      <div className="grid gap-2 border-t pt-3">
        <h5 className="text-xs font-semibold text-foreground">{t("unmatchedSuppressActive")}</h5>
        {suppressions.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("unmatchedSuppressNone")}</p>
        ) : (
          <ul className="grid gap-2">
            {suppressions.map((active) => (
              <li key={`${active.scope}:${active.key}`} className="grid gap-1">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm text-foreground">{active.scope}</span>
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy}
                    onClick={() =>
                      void run(() =>
                        resetUnmatchedSuppression(
                          suppressionTarget(active.scope as SuppressionScope, record),
                        ),
                      )
                    }
                  >
                    {t("unmatchedSuppressReset")}
                  </Button>
                </div>
                <span className="text-xs text-muted-foreground">
                  {t("unmatchedSuppressProvenance", {
                    actor: active.actorProfileID || t("unmatchedSuppressUnknownActor"),
                    when: active.createdAt || "",
                  })}
                  {active.sourceHandle ? ` · ${active.sourceHandle}` : ""}
                </span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
