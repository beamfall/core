import { RefreshCw } from "lucide-react";
import {
  type PointerEvent,
  type ReactNode,
  type RefObject,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import {
  DataTable,
  EmptyState,
  ErrorState,
  StatusChip,
  type StatusTone,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { TableCell, TableRow } from "@/components/ui/table";
import { subscribeEvents } from "@/lib/api";
import {
  fetchHostMetrics,
  fetchLiveSessions,
  type HostMetricSample,
  type LiveSessionInfo,
  terminateSession,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The Console dashboard reads: live sessions with how each one is being
// delivered, and the server's own resource use over the last hour. Everything
// here is read from this server and stays on it.

const SESSIONS_POLL_MS = 5_000;
const METRICS_POLL_MS = 10_000;
const HOUR_MS = 60 * 60 * 1000;

function usePolling(refresh: () => Promise<void>, everyMs: number) {
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (document.visibilityState !== "hidden") void refresh();
    }, everyMs);
    return () => window.clearInterval(timer);
  }, [refresh, everyMs]);
}

const EVENT_SETTLE_MS = 500;

/** Refreshes on server events, at most one read at a time. A new subscription
 * replays retained events and an import emits them in bursts, so events are
 * gathered for a moment and a read in flight finishes before the next one. */
export function useEventRefresh(refresh: () => Promise<void>) {
  useEffect(() => {
    let timer: number | undefined;
    let reading = false;
    let again = false;
    let closed = false;
    const read = async () => {
      timer = undefined;
      if (reading) {
        again = true;
        return;
      }
      reading = true;
      try {
        await refresh();
      } finally {
        reading = false;
        if (again && !closed) {
          again = false;
          timer = window.setTimeout(read, EVENT_SETTLE_MS);
        }
      }
    };
    const unsubscribe = subscribeEvents(() => {
      if (timer === undefined) timer = window.setTimeout(read, EVENT_SETTLE_MS);
    });
    return () => {
      closed = true;
      window.clearTimeout(timer);
      unsubscribe();
    };
  }, [refresh]);
}

export function clock(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${r}` : `${m}:${r}`;
}

/** Bits per second as a short rate: 8.2 Mbps, 640 kbps. */
export function formatBitrate(bitsPerSecond: number): string {
  if (bitsPerSecond >= 1_000_000) {
    const mbps = bitsPerSecond / 1_000_000;
    return `${mbps >= 100 ? mbps.toFixed(0) : mbps.toFixed(1)} Mbps`;
  }
  if (bitsPerSecond >= 1_000) return `${Math.round(bitsPerSecond / 1_000)} kbps`;
  return `${Math.round(bitsPerSecond)} bps`;
}

export function formatBytes(bytes: number): string {
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${gb >= 100 ? gb.toFixed(0) : gb.toFixed(1)} GB`;
  return `${Math.round(bytes / 1024 ** 2)} MB`;
}

const METHOD: Record<string, { label: StringKey; tone: StatusTone }> = {
  direct: { label: "liveSessionDirectPlay", tone: "green" },
  remux: { label: "liveSessionRemux", tone: "cyan" },
  transcode: { label: "liveSessionTranscoding", tone: "amber" },
};

const REASON: Record<string, StringKey> = {
  container: "playReasonContainer",
  "video-codec": "playReasonVideoCodec",
  resolution: "playReasonResolution",
  bitrate: "playReasonBitrate",
  "video-profile": "playReasonVideoProfile",
  hdr: "playReasonHdr",
  audio: "playReasonAudio",
  subtitles: "playReasonSubtitles",
  "device-quirk": "playReasonDeviceQuirk",
  capacity: "playReasonCapacity",
  "cached-rendition": "playReasonCachedRendition",
  "quality-choice": "playReasonQualityChoice",
};

function sessionMethod(s: LiveSessionInfo) {
  return METHOD[s.playMethod] ?? (s.transcoding ? METHOD.transcode : METHOD.direct);
}

function SessionProgress({ session }: { session: LiveSessionInfo }) {
  const t = useT();
  const duration = session.durationSeconds;
  const fraction = duration > 0 ? Math.min(1, Math.max(0, session.positionSeconds / duration)) : 0;
  return (
    <div className="console-session-progress">
      <div
        className="console-jbar"
        role="progressbar"
        aria-label={t("liveSessionProgress")}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(fraction * 100)}
      >
        <i style={{ width: `${fraction * 100}%` }} />
      </div>
      <span className="console-mono">
        {clock(session.positionSeconds)} / {clock(duration)}
      </span>
    </div>
  );
}

// Live Sessions (ADR-0007): active playback across devices, with a server-side
// terminate behind a confirm. Refreshed on every server event and on a short
// timer, so position and delivery stay current while the page is open. A
// restricted session the operator has not revealed shows its load without its
// media (the server omits the title and the reason) — never hidden.
export function LiveSessions() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchLiveSessions);
  const sessions = data ?? [];

  useEventRefresh(refresh);
  usePolling(refresh, SESSIONS_POLL_MS);

  async function terminate(sessionID: string) {
    await terminateSession(sessionID);
    await refresh();
  }

  return (
    <Section
      heading="liveSessionsHeading"
      lead="liveSessionsLead"
      actions={
        <Button variant="outline" size="sm" onClick={() => void refresh()}>
          <RefreshCw className="console-nav-icon" aria-hidden />
          {t("liveSessionsRefresh")}
        </Button>
      }
    >
      {loading && !data ? (
        <Skeleton className="h-32 w-full" />
      ) : failed && sessions.length === 0 ? (
        <ErrorState message="liveSessionsUnavailable" />
      ) : sessions.length === 0 ? (
        <EmptyState message="liveSessionsEmpty" />
      ) : (
        <DataTable
          columns={[
            { key: "profile", label: t("profile") },
            { key: "title", label: t("title") },
            { key: "device", label: t("deviceName") },
            { key: "method", label: t("liveSessionMethod") },
            { key: "bitrate", label: t("liveSessionBitrate") },
            { key: "progress", label: t("liveSessionProgress") },
            { key: "action", label: "" },
          ]}
        >
          {sessions.map((s) => {
            const method = sessionMethod(s);
            const reason = REASON[s.playReason];
            return (
              <TableRow key={s.sessionID}>
                <TableCell className="font-medium">{s.profileName}</TableCell>
                <TableCell>
                  {s.title ? (
                    s.title
                  ) : (
                    <StatusChip tone="iris">{t("liveSessionRestricted")}</StatusChip>
                  )}
                </TableCell>
                <TableCell>{s.deviceName}</TableCell>
                <TableCell>
                  <div className="console-session-method">
                    <StatusChip tone={method.tone}>{t(method.label)}</StatusChip>
                    {reason && <span className="console-session-reason">{t(reason)}</span>}
                  </div>
                </TableCell>
                <TableCell className="console-mono">
                  {s.bitrate > 0 ? formatBitrate(s.bitrate) : "—"}
                </TableCell>
                <TableCell>
                  <SessionProgress session={s} />
                </TableCell>
                <TableCell className="text-right">
                  <ConfirmButton
                    size="sm"
                    label={t("terminateSession")}
                    confirm="terminateSession"
                    onConfirm={() => terminate(s.sessionID)}
                  />
                </TableCell>
              </TableRow>
            );
          })}
        </DataTable>
      )}
    </Section>
  );
}

/** The Overview tile's live figure: how many sessions are playing right now. */
export function LiveSessionCount() {
  const { data, refresh } = useAsync(fetchLiveSessions);
  useEventRefresh(refresh);
  usePolling(refresh, SESSIONS_POLL_MS);
  const count = data?.length;
  return (
    <span className="console-status-value">
      <span className="console-status-dot" data-tone={count ? "green" : "neutral"} aria-hidden />
      {count === undefined ? "–" : count}
    </span>
  );
}

// --- Resource graphs --------------------------------------------------------

interface Series {
  label: string;
  /** A CSS color (a design token) for the line and its legend swatch. */
  color: string;
  /** A second encoding beside color, so two lines never differ by hue alone. */
  dashed?: boolean;
  /** One value per sample; null leaves a gap. */
  values: (number | null)[];
}

interface ChartProps {
  title: string;
  /** The current reading, shown beside the title in text ink. */
  headline: string;
  times: number[];
  series: Series[];
  /** The top of the y axis. */
  max: number;
  format: (value: number) => string;
}

const CHART_HEIGHT = 150;
const PAD = { top: 8, right: 8, bottom: 20, left: 8 };

function useWidth(fallback: number): [RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement | null>(null);
  const [width, setWidth] = useState(fallback);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const next = Math.round(entries[0]?.contentRect.width ?? 0);
      if (next > 0) setWidth(next);
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [ref, width];
}

function timeOfDay(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/** One measure against time, on one axis: 2px lines, a hover crosshair that
 * reads every series at the nearest sample, and the same data as a table. */
function LineChart({ title, headline, times, series, max, format }: ChartProps) {
  const t = useT();
  const titleID = useId();
  const [wrapRef, width] = useWidth(420);
  const [hover, setHover] = useState<number | null>(null);

  const end = times[times.length - 1] ?? Date.now();
  const start = end - HOUR_MS;
  const plotW = Math.max(1, width - PAD.left - PAD.right);
  const plotH = CHART_HEIGHT - PAD.top - PAD.bottom;
  const x = (ms: number) => PAD.left + ((ms - start) / HOUR_MS) * plotW;
  const y = (v: number) => PAD.top + plotH - (Math.min(v, max) / max) * plotH;

  function path(values: (number | null)[]): string {
    let d = "";
    let pen = false;
    values.forEach((v, i) => {
      if (v === null || times[i] < start) {
        pen = false;
        return;
      }
      d += `${pen ? "L" : "M"}${x(times[i]).toFixed(1)} ${y(v).toFixed(1)}`;
      pen = true;
    });
    return d;
  }

  function onMove(event: PointerEvent<SVGSVGElement>) {
    if (times.length === 0) return;
    const box = event.currentTarget.getBoundingClientRect();
    const at = start + ((event.clientX - box.left - PAD.left) / plotW) * HOUR_MS;
    let nearest = 0;
    for (let i = 1; i < times.length; i++) {
      if (Math.abs(times[i] - at) < Math.abs(times[nearest] - at)) nearest = i;
    }
    setHover(nearest);
  }

  const hoverX = hover !== null && times[hover] !== undefined ? x(times[hover]) : null;
  const rows = tableRows(times);

  return (
    <figure className="console-chart console-card console-card-pad" aria-labelledby={titleID}>
      <figcaption className="console-chart-head">
        <span id={titleID} className="console-chart-title">
          {title}
        </span>
        <span className="console-chart-now console-mono">{headline}</span>
      </figcaption>
      {series.length > 1 && (
        <ul className="console-chart-legend">
          {series.map((s) => (
            <li key={s.label}>
              <svg width="18" height="8" aria-hidden="true">
                <line
                  x1="0"
                  y1="4"
                  x2="18"
                  y2="4"
                  stroke={s.color}
                  strokeWidth="2"
                  strokeDasharray={s.dashed ? "5 3" : undefined}
                />
              </svg>
              {s.label}
            </li>
          ))}
        </ul>
      )}
      <div className="console-chart-plot" ref={wrapRef}>
        <svg
          width={width}
          height={CHART_HEIGHT}
          role="img"
          aria-labelledby={titleID}
          onPointerMove={onMove}
          onPointerLeave={() => setHover(null)}
        >
          {[0, 0.5, 1].map((f) => (
            <line
              key={f}
              className="console-chart-grid"
              x1={PAD.left}
              x2={PAD.left + plotW}
              y1={PAD.top + plotH * (1 - f)}
              y2={PAD.top + plotH * (1 - f)}
            />
          ))}
          <text className="console-chart-tick" x={PAD.left + 2} y={PAD.top + 11}>
            {format(max)}
          </text>
          <text className="console-chart-tick" x={PAD.left} y={CHART_HEIGHT - 5}>
            {t("hostMetricsMinutesAgo", { minutes: 60 })}
          </text>
          <text
            className="console-chart-tick"
            x={PAD.left + plotW / 2}
            y={CHART_HEIGHT - 5}
            textAnchor="middle"
          >
            {t("hostMetricsMinutesAgo", { minutes: 30 })}
          </text>
          <text
            className="console-chart-tick"
            x={PAD.left + plotW}
            y={CHART_HEIGHT - 5}
            textAnchor="end"
          >
            {t("hostMetricsNow")}
          </text>
          {series.map((s) => (
            <path
              key={s.label}
              d={path(s.values)}
              fill="none"
              stroke={s.color}
              strokeWidth="2"
              strokeLinejoin="round"
              strokeLinecap="round"
              strokeDasharray={s.dashed ? "5 3" : undefined}
            />
          ))}
          {hover !== null && hoverX !== null && (
            <g>
              <line
                className="console-chart-crosshair"
                x1={hoverX}
                x2={hoverX}
                y1={PAD.top}
                y2={PAD.top + plotH}
              />
              {series.map((s) => {
                const v = s.values[hover];
                if (v === null || v === undefined) return null;
                return (
                  <circle
                    key={s.label}
                    className="console-chart-dot"
                    cx={hoverX}
                    cy={y(v)}
                    r="4"
                    fill={s.color}
                  />
                );
              })}
            </g>
          )}
        </svg>
        {hover !== null && hoverX !== null && (
          <div
            className="console-chart-tip"
            style={hoverX > width / 2 ? { right: width - hoverX + 10 } : { left: hoverX + 10 }}
          >
            <div className="console-chart-tip-time">{timeOfDay(times[hover])}</div>
            {series.map((s) => {
              const v = s.values[hover];
              return (
                <div key={s.label} className="console-chart-tip-row">
                  <span className="console-chart-swatch" style={{ background: s.color }} />
                  <span>{s.label}</span>
                  <span className="console-mono">
                    {v === null || v === undefined ? "—" : format(v)}
                  </span>
                </div>
              );
            })}
          </div>
        )}
      </div>
      <details className="console-chart-table">
        <summary>{t("hostMetricsShowTable")}</summary>
        <table>
          <thead>
            <tr>
              <th scope="col">{t("hostMetricsTime")}</th>
              {series.map((s) => (
                <th scope="col" key={s.label}>
                  {s.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((i) => (
              <tr key={times[i]}>
                <th scope="row">{timeOfDay(times[i])}</th>
                {series.map((s) => {
                  const v = s.values[i];
                  return <td key={s.label}>{v === null || v === undefined ? "—" : format(v)}</td>;
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  );
}

/** The sample indexes the table view lists: about one row every five minutes,
 * always ending on the newest sample. */
function tableRows(times: number[]): number[] {
  const rows: number[] = [];
  let last = Number.POSITIVE_INFINITY;
  for (let i = times.length - 1; i >= 0; i--) {
    if (last - times[i] >= 5 * 60 * 1000) {
      rows.push(i);
      last = times[i];
    }
  }
  return rows.reverse();
}

/** A round axis top at or above the largest reading, never below `floor`. */
function niceMax(values: (number | null)[], floor: number): number {
  const peak = Math.max(floor, ...values.map((v) => v ?? 0));
  const magnitude = 10 ** Math.floor(Math.log10(peak));
  for (const step of [1, 2, 5, 10]) {
    if (step * magnitude >= peak) return step * magnitude;
  }
  return peak;
}

// CPU, memory and network over the last hour — three charts, one measure each,
// rather than one chart with competing axes.
export function HostGraphs() {
  const t = useT();
  const { data, loading, failed, refresh } = useAsync(fetchHostMetrics);
  usePolling(refresh, METRICS_POLL_MS);
  const samples: HostMetricSample[] = data?.samples ?? [];
  const times = samples.map((s) => Date.parse(s.at));
  const newest = samples[samples.length - 1];

  let body: ReactNode;
  // Polls keep the last readings mounted: only the first load shows a
  // skeleton, so an open table or hover survives a refresh.
  if (loading && !data) {
    body = <Skeleton className="h-48 w-full" />;
  } else if (failed && samples.length === 0) {
    body = <ErrorState message="hostMetricsUnavailable" />;
  } else if (!newest) {
    body = <EmptyState message="hostMetricsCollecting" />;
  } else {
    const cpu = samples.map((s) => (s.cpuKnown ? s.cpuPercent : null));
    const cpuKnown = cpu.some((v) => v !== null);
    const sent = samples.map((s) => s.sentBytesPerSecond * 8);
    const received = samples.map((s) => s.receivedBytesPerSecond * 8);
    // A zero total is a reading the host could not take, not free memory.
    const memory = samples.map((s) => (s.memoryTotalBytes > 0 ? s.memoryUsedBytes : null));
    const memoryKnown = memory.some((v) => v !== null);
    const memoryTotal = Math.max(0, ...samples.map((s) => s.memoryTotalBytes));
    body = (
      <div className="console-chart-grid-wrap">
        {cpuKnown ? (
          <LineChart
            title={t("hostMetricsCpu")}
            headline={newest.cpuKnown ? `${Math.round(newest.cpuPercent)}%` : "—"}
            times={times}
            series={[{ label: t("hostMetricsCpu"), color: "var(--accent)", values: cpu }]}
            max={100}
            format={(v) => `${Math.round(v)}%`}
          />
        ) : (
          <figure className="console-chart console-card console-card-pad">
            <figcaption className="console-chart-head">
              <span className="console-chart-title">{t("hostMetricsCpu")}</span>
            </figcaption>
            <p className="console-chart-missing">{t("hostMetricsCpuUnavailable")}</p>
          </figure>
        )}
        {memoryKnown ? (
          <LineChart
            title={t("hostMetricsMemory")}
            headline={
              newest.memoryTotalBytes > 0
                ? t("hostMetricsOf", {
                    used: formatBytes(newest.memoryUsedBytes),
                    total: formatBytes(newest.memoryTotalBytes),
                  })
                : "—"
            }
            times={times}
            series={[{ label: t("hostMetricsMemory"), color: "var(--accent)", values: memory }]}
            max={memoryTotal}
            format={formatBytes}
          />
        ) : (
          <figure className="console-chart console-card console-card-pad">
            <figcaption className="console-chart-head">
              <span className="console-chart-title">{t("hostMetricsMemory")}</span>
            </figcaption>
            <p className="console-chart-missing">{t("hostMetricsMemoryUnavailable")}</p>
          </figure>
        )}
        <LineChart
          title={t("hostMetricsNetwork")}
          headline={`↑ ${formatBitrate(newest.sentBytesPerSecond * 8)} · ↓ ${formatBitrate(newest.receivedBytesPerSecond * 8)}`}
          times={times}
          series={[
            { label: t("hostMetricsSent"), color: "var(--state-direct)", values: sent },
            {
              label: t("hostMetricsReceived"),
              color: "var(--state-remote)",
              dashed: true,
              values: received,
            },
          ]}
          max={niceMax([...sent, ...received], 1_000_000)}
          format={formatBitrate}
        />
      </div>
    );
  }

  return (
    <Section heading="hostMetricsHeading" lead="hostMetricsLead">
      {body}
    </Section>
  );
}
