// The live panels of the party host page (CORE-2945): monitor and transport,
// stage screens, find-and-add, the rotation with requests, and participants.

import {
  ArrowDown,
  ArrowUp,
  ExternalLink,
  Pause,
  Play,
  Redo2,
  SkipForward,
  Undo2,
} from "lucide-react";
import { type DragEvent, useEffect, useId, useRef, useState } from "react";
import { useHref } from "react-router";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { displayString } from "@/lib/display";
import {
  PARTY_QR_CORNERS,
  type PartyEntry,
  type PartyQRCorner,
  type PartyScreen,
  type PartySession,
  type PlaybackAdjustmentRanges,
  partyAdd,
  partyAdvance,
  partyMove,
  partyPlayback,
  partyRemove,
  partyRemoveParticipant,
  partyRestore,
  partyReview,
  partyScreenDisplay,
  partyScreenDrop,
  partyScreenTicker,
  partySkip,
  type SearchResultItem,
  searchResult,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { formatClockTime } from "../lib/transport";
import { anchorTargetMS, screenInSync, stagePath } from "./partySync";
import type { RunPartyOp } from "./usePartyHost";

const SEEK_STEP_MS = 10_000;
const TEMPO_STEP = 0.05;

type PanelProps = { session: PartySession; run: RunPartyOp };

function minutes(seconds: number): number {
  return Math.max(1, Math.round(seconds / 60));
}

function signedSemitones(semitones: number): string {
  if (semitones > 0) return `+${semitones}`;
  if (semitones < 0) return `−${Math.abs(semitones)}`;
  return "0";
}

function Panel({
  title,
  className = "",
  children,
}: {
  title: string;
  className?: string;
  children: React.ReactNode;
}) {
  const id = useId();
  return (
    <section className={`bf-party-panel flex flex-col gap-3 ${className}`} aria-labelledby={id}>
      <h2 id={id} className="bf-party-h2">
        {title}
      </h2>
      {children}
    </section>
  );
}

/**
 * Skips the current entry and offers an Undo that brings it back as next up.
 * The notice goes once the entry is back in the rotation by any route.
 */
export function useSkipUndo(session: PartySession, run: RunPartyOp) {
  const t = useT();
  const [skippedID, setSkippedID] = useState("");
  const skip = async (entry: PartyEntry) => {
    if (await run(() => partySkip(entry.id))) setSkippedID(entry.id);
  };
  // Read from the live list: a song that has since gone away comes back
  // redacted, and its skip can no longer be undone.
  const undo = session.skipped.find((entry) => entry.id === skippedID && entry.title !== "");
  const notice = undo && (
    <p role="status" className="bf-party-notice flex flex-wrap items-center gap-2" data-tone="ok">
      {t("partyHostSkippedNotice", { name: undo.participantName, title: undo.title })}
      <Button
        variant="outline"
        size="sm"
        onClick={async () => {
          if (await run(() => partyRestore(undo.id))) setSkippedID("");
        }}
      >
        {t("partyHostUndo")}
      </Button>
    </p>
  );
  return { skip, notice };
}

export function PartyMonitor({
  session,
  run,
  ranges,
  serverNow,
}: PanelProps & { ranges: PlaybackAdjustmentRanges | null; serverNow: () => number }) {
  const t = useT();
  const { current, playback } = session;
  const [, setTick] = useState(0);
  const [scrubMS, setScrubMS] = useState<number | null>(null);
  const playing = playback?.state === "playing" && playback.entryID === current?.id;
  const { skip, notice: skipNotice } = useSkipUndo(session, run);

  useEffect(() => {
    if (!playing) return;
    const timer = window.setInterval(() => setTick((n) => n + 1), 250);
    return () => window.clearInterval(timer);
  }, [playing]);

  if (!current) {
    return (
      <Panel title={t("partyHostNowSinging")} className="bf-party-monitor">
        {skipNotice}
        <p className="bf-party-muted">
          {session.entries.length > 0 ? t("partyHostIdleReady") : t("partyHostIdleEmpty")}
        </p>
        {session.entries.length > 0 && (
          <div>
            <Button onClick={() => void run(() => partyAdvance())}>
              <Play className="size-4" aria-hidden />
              {t("partyHostStartFirst")}
            </Button>
          </div>
        )}
      </Panel>
    );
  }

  const live = playback && playback.entryID === current.id ? playback : null;
  const tempo = live?.tempo ?? 1;
  const pitch = live?.pitchSemitones ?? 0;
  const durationMS = current.durationSeconds * 1000;
  const targetMS = live ? anchorTargetMS(live, serverNow()) : 0;
  const shownMS = scrubMS ?? (durationMS > 0 ? Math.min(targetMS, durationMS) : targetMS);

  const send = (
    patch: Partial<{
      state: "playing" | "paused";
      positionMS: number;
      tempo: number;
      pitch: number;
    }>,
  ) =>
    run(() =>
      partyPlayback({
        entryID: current.id,
        state: patch.state ?? (playing ? "playing" : "paused"),
        positionMS: Math.max(0, Math.round(patch.positionMS ?? targetMS)),
        tempo: patch.tempo ?? tempo,
        pitchSemitones: patch.pitch ?? pitch,
      }),
    );

  const commitScrub = () => {
    if (scrubMS === null) return;
    const positionMS = scrubMS;
    setScrubMS(null);
    void send({ positionMS });
  };

  const seekBy = (deltaMS: number) => {
    const next = targetMS + deltaMS;
    void send({ positionMS: durationMS > 0 ? Math.min(next, durationMS) : next });
  };

  return (
    <Panel title={t("partyHostNowSinging")} className="bf-party-monitor">
      <div className="flex min-w-0 flex-col gap-0.5">
        <p className="bf-party-now-title">{current.title}</p>
        <p className="bf-party-muted">
          {[current.subtitle, t("partyHostSungBy", { name: current.participantName })]
            .filter(Boolean)
            .join(" · ")}
        </p>
      </div>
      <div className="flex flex-col gap-1">
        <input
          type="range"
          className="bf-party-scrub"
          aria-label={t("partyHostPosition")}
          min={0}
          max={Math.max(durationMS, shownMS, 1)}
          step={1000}
          value={shownMS}
          onChange={(event) => setScrubMS(Number(event.target.value))}
          onPointerUp={commitScrub}
          onKeyUp={commitScrub}
        />
        <p className="bf-party-muted flex justify-between text-sm tabular-nums">
          <span>{formatClockTime(shownMS / 1000)}</span>
          <span>{durationMS > 0 ? formatClockTime(current.durationSeconds) : ""}</span>
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="icon"
          aria-label={t("partyHostBack10")}
          onClick={() => seekBy(-SEEK_STEP_MS)}
        >
          <Undo2 className="size-4" aria-hidden />
        </Button>
        <Button onClick={() => void send({ state: playing ? "paused" : "playing" })}>
          {playing ? (
            <Pause className="size-4" aria-hidden />
          ) : (
            <Play className="size-4" aria-hidden />
          )}
          {playing ? t("partyHostPause") : t("partyHostPlay")}
        </Button>
        <Button
          variant="outline"
          size="icon"
          aria-label={t("partyHostForward10")}
          onClick={() => seekBy(SEEK_STEP_MS)}
        >
          <Redo2 className="size-4" aria-hidden />
        </Button>
        <Button variant="secondary" onClick={() => void skip(current)}>
          <SkipForward className="size-4" aria-hidden />
          {t("partyHostSkip")}
        </Button>
      </div>
      {skipNotice}
      {ranges && (
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
          <Stepper
            label={t("partyHostKey")}
            value={t("partyHostKeyValue", { key: signedSemitones(pitch) })}
            downLabel={t("partyHostKeyDown")}
            upLabel={t("partyHostKeyUp")}
            canDown={pitch > ranges.pitchShiftSemitonesMin}
            canUp={pitch < ranges.pitchShiftSemitonesMax}
            onStep={(dir) => void send({ pitch: pitch + dir })}
          />
          <Stepper
            label={t("partyHostTempo")}
            value={t("partyHostTempoValue", { percent: Math.round(tempo * 100) })}
            downLabel={t("partyHostTempoDown")}
            upLabel={t("partyHostTempoUp")}
            canDown={tempo - TEMPO_STEP >= ranges.tempoMin - 1e-9}
            canUp={tempo + TEMPO_STEP <= ranges.tempoMax + 1e-9}
            onStep={(dir) =>
              void send({ tempo: Math.round((tempo + dir * TEMPO_STEP) * 100) / 100 })
            }
          />
        </div>
      )}
    </Panel>
  );
}

function Stepper({
  label,
  value,
  downLabel,
  upLabel,
  canDown,
  canUp,
  onStep,
}: {
  label: string;
  value: string;
  downLabel: string;
  upLabel: string;
  canDown: boolean;
  canUp: boolean;
  onStep: (direction: 1 | -1) => void;
}) {
  return (
    <div className="flex items-center gap-2">
      <span className="bf-party-label">{label}</span>
      <Button
        variant="outline"
        size="icon-sm"
        aria-label={downLabel}
        disabled={!canDown}
        onClick={() => onStep(-1)}
      >
        −
      </Button>
      <span className="min-w-12 text-center tabular-nums">{value}</span>
      <Button
        variant="outline"
        size="icon-sm"
        aria-label={upLabel}
        disabled={!canUp}
        onClick={() => onStep(1)}
      >
        +
      </Button>
    </div>
  );
}

const QR_CORNER_LABEL = {
  off: "partyHostQROff",
  "top-left": "partyHostQRTopLeft",
  "top-right": "partyHostQRTopRight",
  "bottom-left": "partyHostQRBottomLeft",
  "bottom-right": "partyHostQRBottomRight",
} as const satisfies Record<PartyQRCorner, string>;

export function PartyScreens({ session, run }: PanelProps) {
  const t = useT();
  const joinHref = useHref(stagePath(session.id, true));
  // The last display each screen was asked for, until its write settles, so a
  // second control sent before the first returns does not undo it.
  const pending = useRef(new Map<string, { qrCorner: PartyQRCorner; muted: boolean }>());
  const [, rerender] = useState(0);
  const displayOf = (screen: PartyScreen) => pending.current.get(screen.id) ?? screen;
  const setDisplay = (
    screen: PartyScreen,
    next: Partial<Pick<PartyScreen, "qrCorner" | "muted">>,
  ) => {
    const current = displayOf(screen);
    const want = { qrCorner: current.qrCorner, muted: current.muted, ...next };
    pending.current.set(screen.id, want);
    rerender((n) => n + 1);
    void run(() => partyScreenDisplay(screen.id, want.qrCorner, want.muted)).finally(() => {
      if (pending.current.get(screen.id) === want) pending.current.delete(screen.id);
      rerender((n) => n + 1);
    });
  };
  return (
    <Panel title={t("partyHostScreens")} className="bf-party-screens">
      {session.screens.length === 0 ? (
        <p className="bf-party-muted">{t("partyHostScreensEmpty")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {session.screens.map((screen) => {
            const inSync = screenInSync(screen);
            const display = displayOf(screen);
            const status =
              screen.reportedAt === ""
                ? t("partyHostScreenWaiting")
                : inSync
                  ? t("partyHostScreenInSync")
                  : t("partyHostScreenOffset", { ms: Math.round(screen.offsetMS) });
            return (
              <li key={screen.id} className="bf-party-row">
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium">{screen.name}</span>
                  <span className="bf-party-muted text-sm">
                    {screen.kind === "popout"
                      ? t("partyHostScreenPopout")
                      : t("partyHostScreenJoined")}
                    {" · "}
                    <span className="bf-party-sync" data-in-sync={inSync}>
                      {status}
                    </span>
                  </span>
                </div>
                <div className="bf-party-screen-controls">
                  <label className="flex items-center gap-2 text-sm">
                    <span>{t("partyHostQRCorner")}</span>
                    <NativeSelect
                      value={display.qrCorner}
                      aria-label={t("partyHostQRCornerOn", { name: screen.name })}
                      onValueChange={(corner) =>
                        setDisplay(screen, { qrCorner: corner as PartyQRCorner })
                      }
                    >
                      {PARTY_QR_CORNERS.map((corner) => (
                        <option key={corner} value={corner}>
                          {t(QR_CORNER_LABEL[corner])}
                        </option>
                      ))}
                    </NativeSelect>
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <Switch
                      checked={display.muted}
                      aria-label={t("partyHostMuteScreen", { name: screen.name })}
                      onCheckedChange={(muted) => setDisplay(screen, { muted })}
                    />
                    {t("partyHostMute")}
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <Switch
                      checked={screen.ticker}
                      onCheckedChange={(ticker) =>
                        void run(() => partyScreenTicker(screen.id, ticker))
                      }
                    />
                    {t("partyHostTicker")}
                  </label>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("partyHostDropScreen", { name: screen.name })}
                  onClick={() => void run(() => partyScreenDrop(screen.id))}
                >
                  {t("partyHostDrop")}
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      <a href={joinHref} target="_blank" rel="noopener" className="bf-party-link text-sm">
        {t("partyHostJoinAsScreen")}
        <ExternalLink className="ml-1 inline size-3.5" aria-hidden />
      </a>
    </Panel>
  );
}

const FIND_PAGE = 200;
const FIND_MAX_PAGES = 10;
const FIND_WANT = 50;

/**
 * Keyword search kept to the party's Libraries. searchResult takes no Library
 * argument, so this pages through the ranked global hits (bounded) until it
 * has FIND_WANT in-party matches or the hits run out, rather than filtering
 * only the first page and missing matches ranked below other Libraries'.
 */
export async function searchPartyLibraries(
  query: string,
  libraries: ReadonlySet<string>,
  signal: AbortSignal,
): Promise<SearchResultItem[]> {
  const found: SearchResultItem[] = [];
  for (let page = 0; page < FIND_MAX_PAGES && found.length < FIND_WANT; page++) {
    const response = await searchResult(query, "keyword", signal, {
      offset: page * FIND_PAGE,
      limit: FIND_PAGE,
    });
    for (const item of response.items) {
      if (item.kind === "media" && item.libraryID !== undefined && libraries.has(item.libraryID)) {
        found.push(item);
      }
    }
    if (!response.hasMore || signal.aborted) break;
  }
  return found.slice(0, FIND_WANT);
}

export function PartyFind({ session, run }: PanelProps) {
  const t = useT();
  const locale = useDisplayLocale();
  const inputRef = useRef<HTMLInputElement>(null);
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<SearchResultItem[]>([]);
  const [searchFailed, setSearchFailed] = useState(false);
  const [adding, setAdding] = useState<SearchResultItem | null>(null);
  const libraryKey = session.libraryIDs.join("\n");

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, select, [contenteditable='true']")) return;
      event.preventDefault();
      inputRef.current?.focus();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  useEffect(() => {
    const query = term.trim();
    if (query === "") {
      setResults([]);
      setSearchFailed(false);
      return;
    }
    const libraries = new Set(libraryKey.split("\n"));
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      searchPartyLibraries(query, libraries, controller.signal)
        .then((items) => {
          if (controller.signal.aborted) return;
          setResults(items);
          setSearchFailed(false);
        })
        .catch(() => {
          if (!controller.signal.aborted) setSearchFailed(true);
        });
    }, 250);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [term, libraryKey]);

  return (
    <Panel title={t("partyHostFind")} className="bf-party-find">
      <Input
        ref={inputRef}
        type="search"
        value={term}
        onChange={(event) => setTerm(event.target.value)}
        placeholder={t("partyHostFindPlaceholder")}
        aria-label={t("partyHostFind")}
      />
      {searchFailed && (
        <p role="alert" className="bf-party-muted">
          {t("partyHostFindError")}
        </p>
      )}
      {term.trim() !== "" && !searchFailed && results.length === 0 && (
        <p className="bf-party-muted">{t("partyHostFindEmpty")}</p>
      )}
      <ul className="flex flex-col gap-2">
        {results.map((item) => {
          const detail = [item.byline?.join(", "), displayString(item.subtitle, locale)]
            .filter(Boolean)
            .join(" · ");
          return (
            <li key={item.id} className="bf-party-row flex-wrap">
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium">{item.title}</span>
                {detail && <span className="bf-party-muted truncate text-sm">{detail}</span>}
              </div>
              <Button
                variant="outline"
                size="sm"
                aria-label={t("partyHostAddFor", { title: item.title })}
                aria-expanded={adding?.id === item.id}
                onClick={() => setAdding(adding?.id === item.id ? null : item)}
              >
                {t("partyHostAdd")}
              </Button>
              {adding?.id === item.id && (
                <AddPicker
                  session={session}
                  onCancel={() => setAdding(null)}
                  onAdd={async (who) => {
                    const ok = await run(
                      () => partyAdd({ mediaID: item.id, ...who }),
                      t("partyHostAdded", { title: item.title }),
                    );
                    if (ok) setAdding(null);
                  }}
                />
              )}
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}

const NEW_SINGER = "";

function AddPicker({
  session,
  onAdd,
  onCancel,
}: {
  session: PartySession;
  onAdd: (who: { participantID?: string; participantName?: string }) => Promise<void>;
  onCancel: () => void;
}) {
  const t = useT();
  const id = useId();
  // Most recent turn first, so the singer the host just heard is at hand.
  const people = [...session.participants].sort((a, b) => b.lastTurnAt.localeCompare(a.lastTurnAt));
  const [choice, setChoice] = useState(people[0]?.id ?? NEW_SINGER);
  const [name, setName] = useState("");
  const isNew = choice === NEW_SINGER;
  const ready = !isNew || name.trim() !== "";

  return (
    <form
      className="bf-party-picker w-full"
      onSubmit={(event) => {
        event.preventDefault();
        if (!ready) return;
        void onAdd(isNew ? { participantName: name.trim() } : { participantID: choice });
      }}
    >
      {people.length > 0 && (
        <NativeSelect value={choice} onValueChange={setChoice} aria-label={t("partyHostSinger")}>
          {people.map((person) => (
            <option key={person.id} value={person.id}>
              {person.name}
            </option>
          ))}
          <option value={NEW_SINGER}>{t("partyHostNewSinger")}</option>
        </NativeSelect>
      )}
      {isNew && (
        <>
          <label htmlFor={`${id}-name`} className="sr-only">
            {t("partyHostSingerName")}
          </label>
          <Input
            id={`${id}-name`}
            value={name}
            maxLength={40}
            onChange={(event) => setName(event.target.value)}
            placeholder={t("partyHostSingerName")}
          />
        </>
      )}
      <Button type="submit" size="sm" disabled={!ready}>
        {t("partyHostAddConfirm")}
      </Button>
      <Button type="button" variant="ghost" size="sm" onClick={onCancel}>
        {t("partyHostCancel")}
      </Button>
    </form>
  );
}

export function PartyRotation({ session, run }: PanelProps) {
  const t = useT();
  const [undo, setUndo] = useState<PartyEntry | null>(null);
  const dragRef = useRef<string | null>(null);
  const waiting = session.entries;
  const totalSeconds = waiting.reduce((sum, entry) => sum + entry.durationSeconds, 0);

  const move = (entry: PartyEntry, position: number) => {
    if (position < 1 || position > waiting.length || position === entry.position) return;
    void run(() => partyMove(entry.id, position));
  };

  const remove = async (entry: PartyEntry) => {
    if (await run(() => partyRemove(entry.id))) setUndo(entry);
  };

  const onDrop = (event: DragEvent, target: PartyEntry) => {
    event.preventDefault();
    const dragged = waiting.find((entry) => entry.id === dragRef.current);
    dragRef.current = null;
    if (dragged) move(dragged, target.position);
  };

  return (
    <Panel title={t("partyHostRotation")} className="bf-party-rotation">
      {session.requests.length > 0 && (
        <div className="bf-party-requests flex flex-col gap-2">
          <h3 className="bf-party-label">
            {t("partyHostRequests", { count: session.requests.length })}
          </h3>
          <ul className="flex flex-col gap-2">
            {session.requests.map((entry) => (
              <li key={entry.id} className="bf-party-row flex-wrap">
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium">{entry.title}</span>
                  {entry.subtitle !== "" && (
                    <span className="bf-party-muted truncate text-sm">{entry.subtitle}</span>
                  )}
                  <span className="bf-party-muted text-sm">
                    {[
                      t("partyHostRequestedBy", { name: entry.participantName }),
                      entry.pitchSemitones !== 0
                        ? t("partyHostEntryKey", { key: signedSemitones(entry.pitchSemitones) })
                        : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </div>
                <Button
                  size="sm"
                  aria-label={t("partyHostApproveTitle", { title: entry.title })}
                  onClick={() => void run(() => partyReview(entry.id, true))}
                >
                  {t("partyHostApprove")}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  aria-label={t("partyHostDeclineTitle", { title: entry.title })}
                  onClick={() => void run(() => partyReview(entry.id, false))}
                >
                  {t("partyHostDecline")}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      )}
      {undo && (
        <p
          role="status"
          className="bf-party-notice flex flex-wrap items-center gap-2"
          data-tone="ok"
        >
          {t("partyHostRemoved", { title: undo.title })}
          <Button
            variant="outline"
            size="sm"
            onClick={async () => {
              if (await run(() => partyRestore(undo.id))) setUndo(null);
            }}
          >
            {t("partyHostUndo")}
          </Button>
        </p>
      )}
      {waiting.length === 0 ? (
        <p className="bf-party-muted">{t("partyHostRotationEmpty")}</p>
      ) : (
        <>
          <p className="bf-party-muted text-sm">
            {t("partyHostRotationTotal", { count: waiting.length, minutes: minutes(totalSeconds) })}
          </p>
          <ol className="flex flex-col gap-2">
            {waiting.map((entry) => (
              <li
                key={entry.id}
                className="bf-party-row"
                draggable
                onDragStart={(event) => {
                  dragRef.current = entry.id;
                  event.dataTransfer.effectAllowed = "move";
                }}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => onDrop(event, entry)}
              >
                <span className="bf-party-pos tabular-nums" aria-hidden>
                  {entry.position}
                </span>
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium">{entry.title}</span>
                  {entry.subtitle !== "" && (
                    <span className="bf-party-muted truncate text-sm">{entry.subtitle}</span>
                  )}
                  <span className="bf-party-muted text-sm">
                    {[
                      entry.participantName,
                      entry.etaSeconds > 0
                        ? t("partyHostEta", { minutes: minutes(entry.etaSeconds) })
                        : t("partyHostEtaNext"),
                      entry.pitchSemitones !== 0
                        ? t("partyHostEntryKey", { key: signedSemitones(entry.pitchSemitones) })
                        : "",
                      entry.lastPlayedAt !== "" ? t("partyHostSungEarlier") : "",
                    ]
                      .filter(Boolean)
                      .join(" · ")}
                  </span>
                </div>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("partyHostMoveUp", { title: entry.title })}
                  disabled={entry.position <= 1}
                  onClick={() => move(entry, entry.position - 1)}
                >
                  <ArrowUp className="size-4" aria-hidden />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("partyHostMoveDown", { title: entry.title })}
                  disabled={entry.position >= waiting.length}
                  onClick={() => move(entry, entry.position + 1)}
                >
                  <ArrowDown className="size-4" aria-hidden />
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("partyHostRemoveTitle", { title: entry.title })}
                  onClick={() => void remove(entry)}
                >
                  {t("partyHostRemove")}
                </Button>
              </li>
            ))}
          </ol>
        </>
      )}
      {session.skipped.length > 0 && (
        <div className="bf-party-skipped flex flex-col gap-2">
          <h3 className="bf-party-label">{t("partyHostSkippedHeading")}</h3>
          <ul className="flex flex-col gap-2">
            {session.skipped.map((entry) => (
              <li key={entry.id} className="bf-party-row">
                <div className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium">{entry.participantName}</span>
                  <span className="bf-party-muted truncate text-sm">
                    {entry.title || t("partyHostSkippedUnavailable")}
                  </span>
                </div>
                {/* The server redacts the title of Media the session can no longer see; such a skip cannot come back. */}
                {entry.title !== "" && (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={t("partyHostBringBackTitle", {
                      name: entry.participantName,
                      title: entry.title,
                    })}
                    onClick={() => void run(() => partyRestore(entry.id))}
                  >
                    {t("partyHostBringBack")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}

export function PartyPeople({ session, run }: PanelProps) {
  const t = useT();
  return (
    <Panel title={t("partyHostParticipants")}>
      {session.participants.length === 0 ? (
        <p className="bf-party-muted">{t("partyHostParticipantsEmpty")}</p>
      ) : (
        <ul className="bf-party-people">
          {session.participants.map((person) => (
            <li key={person.id} className="bf-party-row">
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="truncate font-medium">{person.name}</span>
                <span className="bf-party-muted text-sm">
                  {[
                    person.guest ? t("partyHostGuest") : "",
                    t("partyHostWaiting", { count: person.waiting }),
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </div>
              <Button
                variant="ghost"
                size="sm"
                aria-label={t("partyHostRemoveTitle", { title: person.name })}
                onClick={() => void run(() => partyRemoveParticipant(person.id))}
              >
                {t("partyHostRemove")}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
