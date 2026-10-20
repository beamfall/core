import { Database, RefreshCw } from "lucide-react";
import { useState } from "react";
import { EmptyState, ErrorState, StatusChip } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  evictPlaybackRenditionCache,
  fetchPlaybackCache,
  precomputePlaybackRenditions,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Human-readable disk size for the budget meter. Binary units (the budget is a
// byte count); a single significant decimal keeps the snapshot scannable.
function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exp = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exp;
  const text = exp === 0 ? String(value) : value.toFixed(1);
  return `${text} ${units[exp]}`;
}

// One status count tile in the health snapshot grid.
function StatTile({ label, value }: { label: StringKey; value: number }) {
  const t = useT();
  return (
    <div className="console-card console-card-pad">
      <div className="console-control-label">{t(label)}</div>
      <div className="mt-1 text-[18px] font-semibold leading-none console-num">{value}</div>
    </div>
  );
}

// Playback Rendition Cache (ADR-0057, PLAY-7): the operator cache-health
// snapshot (object counts + disk-vs-budget) plus the two explicit operator
// actions — per-Media precompute generation (§2, never automatic) and an
// over-budget eviction pass (§4, destructive → confirm). Admin-session gated by
// the Console graph dispatch. When the cache root is unconfigured the snapshot
// reports `configured: false`: the panel renders the unconfigured state and the
// writes are suppressed (the server would refuse them anyway).
export function PlaybackCache() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchPlaybackCache);
  const [mediaID, setMediaID] = useState("");

  async function onPrecompute() {
    const target = mediaID.trim();
    if (!target) return;
    await precomputePlaybackRenditions(target);
    setMediaID("");
    await refresh();
  }

  async function onEvict() {
    await evictPlaybackRenditionCache();
    await refresh();
  }

  const overBudget = data != null && data.budgetBytes > 0 && data.readyBytes > data.budgetBytes;
  const pct =
    data != null && data.budgetBytes > 0
      ? Math.min(100, Math.round((data.readyBytes / data.budgetBytes) * 100))
      : 0;

  return (
    <Section
      heading="playbackCacheHeading"
      lead="playbackCacheLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("playbackCacheRefresh")}
        </Button>
      }
    >
      {loading ? (
        <Skeleton className="h-48 w-full" />
      ) : failed ? (
        <ErrorState message="playbackCacheUnavailable" />
      ) : !data?.configured ? (
        <div className="space-y-1">
          <EmptyState message="playbackCacheUnconfigured" icon={Database} />
          <p className="px-1 text-[12px] leading-snug text-muted-foreground">
            {t("playbackCacheUnconfiguredHelp")}
          </p>
        </div>
      ) : (
        <div className="space-y-4">
          <div className="flex items-center gap-2">
            <StatusChip tone={overBudget ? "amber" : "green"}>
              {t(overBudget ? "playbackCacheOverBudget" : "playbackCacheConfigured")}
            </StatusChip>
          </div>

          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <StatTile label="playbackCacheStatusReady" value={data.ready} />
            <StatTile label="playbackCacheStatusGenerating" value={data.generating} />
            <StatTile label="playbackCacheStatusFailed" value={data.failed} />
            <StatTile label="playbackCacheStatusEvicted" value={data.evicted} />
          </div>

          <div className="console-meter">
            <div className="console-meter-head">
              <span className="console-control-label">{t("playbackCacheDiskUsed")}</span>
              <span className="console-mono">
                {formatBytes(data.readyBytes)} / {formatBytes(data.budgetBytes)}
              </span>
            </div>
            <div
              className="console-meter-track"
              role="progressbar"
              aria-valuenow={pct}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label={t("playbackCacheDiskUsed")}
            >
              <i className={overBudget ? "warn" : undefined} style={{ width: `${pct}%` }} />
            </div>
          </div>

          <div className="console-control-row">
            <div className="min-w-0">
              <div className="console-control-label">{t("playbackCachePrecompute")}</div>
              <p className="console-control-help">{t("playbackCachePrecomputeHelp")}</p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <Input
                value={mediaID}
                onChange={(e) => setMediaID(e.target.value)}
                placeholder={t("playbackCacheMediaPlaceholder")}
                aria-label={t("playbackCacheMediaPlaceholder")}
                className="w-36"
              />
              <Button
                size="sm"
                disabled={mediaID.trim() === ""}
                onClick={() => void onPrecompute()}
              >
                {t("playbackCachePrecomputeAction")}
              </Button>
            </div>
          </div>

          <div className="console-control-row">
            <div className="min-w-0">
              <div className="console-control-label">{t("playbackCacheEvict")}</div>
              <p className="console-control-help">{t("playbackCacheEvictHelp")}</p>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <ConfirmButton
                size="sm"
                label={t("playbackCacheEvictAction")}
                confirm="playbackCacheEvictConfirm"
                onConfirm={onEvict}
              />
            </div>
          </div>
        </div>
      )}
    </Section>
  );
}
