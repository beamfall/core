import { Search } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useId, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";
import {
  FieldHint,
  FieldInput,
  FieldLabel,
  GlassCard,
  HairlineList,
  PageShell,
  PrimaryAction,
  SecondaryAction,
  SurfaceHeading,
  SurfaceSection,
} from "@/components/beamfall/surface";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import {
  cancelPartyRequest,
  fetchGuestState,
  forgetGuest,
  joinParty,
  loadGuest,
  normalizePartyCode,
  type PartyGuestEntry,
  PartyGuestError,
  type PartyGuestState,
  type PartySearchItem,
  requestPartyMedia,
  saveGuest,
  searchPartyMedia,
} from "@/lib/partyGuest";

// The guest phone page of a party session (CORE-2948, design "Guest HTTP" of CORE-1662). Reached
// from the QR code at /web/join/<code>: no device enrollment, no Who's-watching. The guest enters
// a name, gets a scoped token, then finds songs, requests them and watches their place in the
// rotation. The server is the authority for positions and ETAs; the page polls it every few
// seconds while visible. A refused token (session ended, guest removed) drops back to the join
// form with a message.

const POLL_MS = 3000;
const ROTATION_GLANCE = 5;
// Cooldown after a 429 that carried no Retry-After header.
const DEFAULT_RETRY_SECONDS = 5;

type Joined = { code: string; token: string; name: string };

type T = ReturnType<typeof useT>;

function joinErrorMessage(err: unknown, t: T): string {
  if (!(err instanceof PartyGuestError)) return t("partyGuestErrorFailed");
  switch (err.kind) {
    case "unknown-code":
      return t("partyGuestErrorUnknownCode");
    case "rate-limited":
      return t("partyGuestErrorRateLimited");
    case "full":
      return t("partyGuestErrorFull");
    case "ended":
      return t("partyGuestErrorEnded");
    default:
      return t("partyGuestErrorFailed");
  }
}

const isEnded = (err: unknown) => err instanceof PartyGuestError && err.kind === "ended";

export function PartyGuestPage() {
  const { code: routeCode = "" } = useParams();
  const navigate = useNavigate();
  const [joined, setJoined] = useState<Joined | null>(() => {
    const code = normalizePartyCode(routeCode);
    const stored = code ? loadGuest(code) : null;
    return stored ? { code, ...stored } : null;
  });
  const [notice, setNotice] = useState<string | null>(null);
  const t = useT();

  const onEnded = useCallback(() => {
    if (joined) forgetGuest(joined.code);
    setJoined(null);
    setNotice(t("partyGuestErrorEnded"));
  }, [joined, t]);

  return (
    <PageShell>
      {joined ? (
        <JoinedView
          joined={joined}
          onEnded={onEnded}
          onLeave={() => {
            forgetGuest(joined.code);
            setJoined(null);
            setNotice(null);
          }}
        />
      ) : (
        <JoinForm
          initialCode={normalizePartyCode(routeCode)}
          notice={notice}
          onJoined={(next) => {
            saveGuest(next.code, { token: next.token, name: next.name });
            setNotice(null);
            setJoined(next);
            if (next.code !== normalizePartyCode(routeCode)) {
              navigate(`/join/${encodeURIComponent(next.code)}`, { replace: true });
            }
          }}
        />
      )}
    </PageShell>
  );
}

function JoinForm({
  initialCode,
  notice,
  onJoined,
}: {
  initialCode: string;
  notice: string | null;
  onJoined: (joined: Joined) => void;
}) {
  const t = useT();
  const codeID = useId();
  const nameID = useId();
  const nameHintID = useId();
  const errorID = useId();
  const errorRef = useRef<HTMLParagraphElement>(null);
  const [code, setCode] = useState(initialCode);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(notice);
  const [busy, setBusy] = useState(false);

  useEffect(() => setError(notice), [notice]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    const trimmedCode = normalizePartyCode(code);
    const trimmedName = name.trim();
    if (!trimmedCode || !trimmedName) {
      setError(t(trimmedCode ? "partyGuestNameRequired" : "partyGuestCodeRequired"));
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const result = await joinParty(trimmedCode, trimmedName);
      onJoined({ code: trimmedCode, token: result.token, name: result.name || trimmedName });
    } catch (err) {
      setError(joinErrorMessage(err, t));
      setBusy(false);
      requestAnimationFrame(() => errorRef.current?.focus());
    }
  }

  return (
    <GlassCard>
      <SurfaceHeading title={t("partyGuestJoinTitle")} lead={t("partyGuestJoinLead")} />
      <form onSubmit={onSubmit} className="space-y-5" noValidate>
        <div>
          <FieldLabel htmlFor={codeID}>{t("partyGuestCodeLabel")}</FieldLabel>
          <FieldInput
            id={codeID}
            value={code}
            onChange={(e) => {
              setCode(e.target.value.toUpperCase());
              setError(null);
            }}
            className="font-mono tracking-widest uppercase"
            autoComplete="off"
            autoCapitalize="characters"
            autoCorrect="off"
            spellCheck={false}
            required
            aria-invalid={error != null && !code.trim()}
          />
        </div>
        <div>
          <FieldLabel htmlFor={nameID}>{t("partyGuestNameLabel")}</FieldLabel>
          <FieldInput
            id={nameID}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setError(null);
            }}
            maxLength={40}
            autoComplete="nickname"
            autoFocus={initialCode !== ""}
            required
            aria-describedby={error ? `${nameHintID} ${errorID}` : nameHintID}
          />
          <FieldHint id={nameHintID}>{t("partyGuestNameHint")}</FieldHint>
        </div>
        {error && (
          <p
            id={errorID}
            ref={errorRef}
            tabIndex={-1}
            role="alert"
            className="m-0 text-sm font-medium text-destructive outline-none"
          >
            {error}
          </p>
        )}
        <PrimaryAction type="submit" disabled={busy} className="w-full">
          {t(busy ? "partyGuestJoining" : "partyGuestJoin")}
        </PrimaryAction>
      </form>
    </GlassCard>
  );
}

function usePartyState(token: string, onEnded: () => void) {
  const [state, setState] = useState<PartyGuestState | null>(null);
  const [offline, setOffline] = useState(false);
  const endedRef = useRef(onEnded);
  endedRef.current = onEnded;
  const controllerRef = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    try {
      const next = await fetchGuestState(token, controller.signal);
      if (controller.signal.aborted) return;
      setState(next);
      setOffline(false);
    } catch (err) {
      if (controller.signal.aborted) return;
      if (isEnded(err)) endedRef.current();
      else setOffline(true);
    }
  }, [token]);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;
    const visible = () => document.visibilityState !== "hidden";
    const tick = async () => {
      if (visible()) await refresh();
      if (!stopped) timer = setTimeout(tick, POLL_MS);
    };
    const onVisibility = () => {
      if (visible()) void refresh();
    };
    void tick();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      stopped = true;
      clearTimeout(timer);
      controllerRef.current?.abort();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [refresh]);

  return { state, offline, refresh };
}

function etaLabel(entry: PartyGuestEntry, t: T): string {
  if (entry.position === 1 && entry.etaSeconds < 60) return t("partyGuestUpNext");
  return t("partyGuestEta", { minutes: Math.max(1, Math.round(entry.etaSeconds / 60)) });
}

function JoinedView({
  joined,
  onEnded,
  onLeave,
}: {
  joined: Joined;
  onEnded: () => void;
  onLeave: () => void;
}) {
  const t = useT();
  const { state, offline, refresh } = usePartyState(joined.token, onEnded);
  // The confirmation for the last request, shown while that entry is still pending or waiting.
  const [status, setStatus] = useState<{ text: string; entryID: string } | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const mine = state?.mine ?? [];
  const active = mine.filter((e) => e.state === "pending" || e.state === "waiting").length;
  const atLimit = state != null && state.maxWaiting > 0 && active >= state.maxWaiting;
  // Media this guest already asked for (pending, waiting or declined): search marks them Requested.
  const requestedIDs = new Set(mine.flatMap((e) => (e.mediaID ? [e.mediaID] : [])));
  const statusText =
    status &&
    mine.some((e) => e.id === status.entryID && (e.state === "pending" || e.state === "waiting"))
      ? status.text
      : null;
  // After a 429, Request stays disabled until the server's Retry-After has passed.
  const [coolingDown, setCoolingDown] = useState(false);
  const cooldownTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(cooldownTimer.current), []);

  function startCooldown(seconds: number | null) {
    clearTimeout(cooldownTimer.current);
    setCoolingDown(true);
    cooldownTimer.current = setTimeout(
      () => setCoolingDown(false),
      (seconds ?? DEFAULT_RETRY_SECONDS) * 1000,
    );
  }

  function handleActionError(err: unknown, fallback: Parameters<T>[0]) {
    if (isEnded(err)) return onEnded();
    if (err instanceof PartyGuestError && err.kind === "rate-limited") {
      startCooldown(err.retryAfterSeconds);
      setActionError(t("partyGuestRequestRateLimited"));
    } else if (err instanceof PartyGuestError && err.kind === "limit" && state) {
      setActionError(t("partyGuestAtLimit", { max: state.maxWaiting }));
    } else if (err instanceof PartyGuestError && err.kind === "full") {
      setActionError(t("partyGuestErrorFull"));
    } else {
      setActionError(t(fallback));
    }
  }

  async function onRequest(item: PartySearchItem): Promise<boolean> {
    setActionError(null);
    setStatus(null);
    try {
      const entry = await requestPartyMedia(joined.token, item.mediaID);
      setStatus({
        text: t(
          entry.state === "pending"
            ? "partyGuestRequestSentPending"
            : "partyGuestRequestSentWaiting",
        ),
        entryID: entry.id,
      });
      await refresh();
      return true;
    } catch (err) {
      handleActionError(err, "partyGuestRequestFailed");
      return false;
    }
  }

  async function onCancel(entry: PartyGuestEntry) {
    setActionError(null);
    setStatus(null);
    try {
      await cancelPartyRequest(joined.token, entry.id);
      void refresh();
    } catch (err) {
      handleActionError(err, "partyGuestCancelFailed");
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3 px-1">
        <p className="m-0 min-w-0 truncate text-sm text-muted-foreground">
          {t("partyGuestJoinedAs", { name: joined.name })}
        </p>
        <SecondaryAction onClick={onLeave} className="shrink-0 px-4">
          {t("partyGuestLeave")}
        </SecondaryAction>
      </div>
      {offline && (
        <p role="status" className="m-0 px-1 text-sm text-muted-foreground">
          {t("partyGuestReconnecting")}
        </p>
      )}
      <GlassCard className="p-5">
        <NowPlaying current={state?.current ?? null} />
        <SurfaceSection>
          <h2 className="m-0 mb-3 text-base font-semibold">{t("partyGuestYourSongs")}</h2>
          {mine.length === 0 ? (
            <p className="m-0 text-sm text-muted-foreground">{t("partyGuestYourSongsEmpty")}</p>
          ) : (
            <HairlineList>
              {mine.map((entry) => (
                <MyEntry key={entry.id} entry={entry} onCancel={() => onCancel(entry)} />
              ))}
            </HairlineList>
          )}
          <div aria-live="polite" className="mt-3 text-sm empty:hidden">
            {statusText && <p className="m-0 text-muted-foreground">{statusText}</p>}
          </div>
          {actionError && (
            <p role="alert" className="m-0 mt-3 text-sm font-medium text-destructive">
              {actionError}
            </p>
          )}
        </SurfaceSection>
      </GlassCard>
      <GlassCard className="p-5">
        <SearchPanel
          token={joined.token}
          requestedIDs={requestedIDs}
          limitReason={
            atLimit && state
              ? t("partyGuestAtLimit", { max: state.maxWaiting })
              : coolingDown
                ? t("partyGuestRequestRateLimited")
                : null
          }
          onRequest={onRequest}
          onEnded={onEnded}
        />
      </GlassCard>
      <GlassCard className="p-5">
        <Rotation entries={state?.entries ?? []} name={joined.name} mine={mine} />
      </GlassCard>
    </div>
  );
}

function NowPlaying({ current }: { current: PartyGuestEntry | null }) {
  const t = useT();
  return (
    <section aria-labelledby="party-guest-now">
      <h2
        id="party-guest-now"
        className="m-0 mb-2 text-xs font-medium uppercase tracking-wide text-muted-foreground"
      >
        {t("partyGuestNowPlaying")}
      </h2>
      {current ? (
        <div className="min-w-0">
          <p className="m-0 break-words text-xl font-semibold leading-tight">{current.title}</p>
          {current.subtitle && (
            <p className="m-0 mt-1 break-words text-sm text-muted-foreground">{current.subtitle}</p>
          )}
          <p className="m-0 mt-2 text-sm">
            {t("partyGuestSungBy", { name: current.participantName })}
          </p>
        </div>
      ) : (
        <p className="m-0 text-sm text-muted-foreground">{t("partyGuestNowPlayingNone")}</p>
      )}
    </section>
  );
}

function MyEntry({ entry, onCancel }: { entry: PartyGuestEntry; onCancel: () => void }) {
  const t = useT();
  const [busy, setBusy] = useState(false);
  const cancellable = entry.state === "pending" || entry.state === "waiting";
  return (
    <li className="flex items-center gap-3 py-3">
      <div className="min-w-0 flex-1">
        <p className="m-0 break-words font-medium">{entry.title}</p>
        {entry.subtitle && (
          <p className="m-0 break-words text-sm text-muted-foreground">{entry.subtitle}</p>
        )}
        <p className="m-0 mt-1 text-sm">
          {entry.state === "waiting" && entry.position > 0 ? (
            <>
              {t("partyGuestPosition", { position: entry.position })}
              <span aria-hidden> · </span>
              <span className="text-muted-foreground">{etaLabel(entry, t)}</span>
            </>
          ) : entry.state === "pending" ? (
            <Badge variant="secondary">{t("partyGuestPending")}</Badge>
          ) : entry.state === "declined" ? (
            <span className="text-muted-foreground">{t("partyGuestDeclined")}</span>
          ) : null}
        </p>
      </div>
      {cancellable && (
        <Button
          type="button"
          variant="outline"
          className="h-11 shrink-0 px-4"
          disabled={busy}
          aria-label={t("partyGuestCancelLabel", { title: entry.title })}
          onClick={async () => {
            setBusy(true);
            await onCancel();
            setBusy(false);
          }}
        >
          {t("partyGuestCancel")}
        </Button>
      )}
    </li>
  );
}

function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

function SearchPanel({
  token,
  requestedIDs,
  limitReason,
  onRequest,
  onEnded,
}: {
  token: string;
  requestedIDs: Set<string>;
  limitReason: string | null;
  onRequest: (item: PartySearchItem) => Promise<boolean>;
  onEnded: () => void;
}) {
  const t = useT();
  const inputID = useId();
  const limitID = useId();
  const [query, setQuery] = useState("");
  const [submitted, setSubmitted] = useState<string | null>(null);
  const [items, setItems] = useState<PartySearchItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<string | null>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const q = query.trim();
    if (!q || busy) return;
    setBusy(true);
    setError(null);
    try {
      setItems(await searchPartyMedia(token, q));
      setSubmitted(q);
    } catch (err) {
      if (isEnded(err)) return onEnded();
      setError(t("partyGuestSearchFailed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby={`${inputID}-heading`}>
      <h2 id={`${inputID}-heading`} className="m-0 mb-3 text-base font-semibold">
        {t("partyGuestSearchLabel")}
      </h2>
      <form role="search" onSubmit={onSubmit} className="flex gap-2">
        <FieldLabel htmlFor={inputID} className="sr-only">
          {t("partyGuestSearchLabel")}
        </FieldLabel>
        <FieldInput
          id={inputID}
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("partyGuestSearchPlaceholder")}
          enterKeyHint="search"
          autoComplete="off"
          className="min-w-0 flex-1"
        />
        <PrimaryAction
          type="submit"
          disabled={busy || !query.trim()}
          aria-label={t("partyGuestSearch")}
          className="shrink-0 px-4"
        >
          <Search aria-hidden className="size-5" />
        </PrimaryAction>
      </form>
      <div aria-live="polite" className="mt-3 text-sm empty:hidden">
        {busy && <p className="m-0 text-muted-foreground">{t("partyGuestSearching")}</p>}
        {!busy && submitted != null && items.length === 0 && !error && (
          <p className="m-0 text-muted-foreground">
            {t("partyGuestSearchEmpty", { query: submitted })}
          </p>
        )}
      </div>
      {error && (
        <p role="alert" className="m-0 mt-3 text-sm font-medium text-destructive">
          {error}
        </p>
      )}
      {limitReason && items.length > 0 && (
        <p id={limitID} className="m-0 mt-3 text-sm text-muted-foreground">
          {limitReason}
        </p>
      )}
      {items.length > 0 && (
        <HairlineList className="mt-3">
          {items.map((item) => {
            const done = requestedIDs.has(item.mediaID);
            return (
              <li key={item.mediaID} className="flex items-center gap-3 py-3">
                <div className="min-w-0 flex-1">
                  <p className="m-0 break-words font-medium">{item.title}</p>
                  <p className="m-0 break-words text-sm text-muted-foreground">
                    {[item.subtitle, formatDuration(item.durationSeconds)]
                      .filter(Boolean)
                      .join(" · ")}
                  </p>
                  {item.lastPlayedAt && (
                    <p className="m-0 mt-1 text-xs text-muted-foreground">
                      {t("partyGuestSungEarlier")}
                    </p>
                  )}
                </div>
                <Button
                  type="button"
                  variant={done ? "outline" : "default"}
                  className="h-11 shrink-0 px-4"
                  disabled={done || limitReason != null || pending != null}
                  aria-label={done ? undefined : t("partyGuestRequestLabel", { title: item.title })}
                  aria-describedby={limitReason && !done ? limitID : undefined}
                  onClick={async () => {
                    setPending(item.mediaID);
                    await onRequest(item);
                    setPending(null);
                  }}
                >
                  {t(done ? "partyGuestRequested" : "partyGuestRequest")}
                </Button>
              </li>
            );
          })}
        </HairlineList>
      )}
    </section>
  );
}

function Rotation({
  entries,
  name,
  mine,
}: {
  entries: PartyGuestEntry[];
  name: string;
  mine: PartyGuestEntry[];
}) {
  const t = useT();
  const mineIDs = new Set(mine.map((e) => e.id));
  const glance = entries.slice(0, ROTATION_GLANCE);
  return (
    <section aria-labelledby="party-guest-rotation">
      <h2 id="party-guest-rotation" className="m-0 mb-3 text-base font-semibold">
        {t("partyGuestRotation")}
      </h2>
      {glance.length === 0 ? (
        <p className="m-0 text-sm text-muted-foreground">{t("partyGuestRotationEmpty")}</p>
      ) : (
        <HairlineList>
          {glance.map((entry) => {
            const isMine = mineIDs.has(entry.id);
            return (
              <li key={entry.id} className="flex items-baseline gap-3 py-2.5">
                <span className="w-6 shrink-0 text-right text-sm tabular-nums text-muted-foreground">
                  {entry.position}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="m-0 truncate text-sm font-medium">
                    {isMine
                      ? `${entry.participantName || name} (${t("partyGuestYou")})`
                      : entry.participantName}
                  </p>
                  <p className="m-0 truncate text-sm text-muted-foreground">{entry.title}</p>
                </div>
                <span className="shrink-0 text-sm text-muted-foreground">{etaLabel(entry, t)}</span>
              </li>
            );
          })}
        </HairlineList>
      )}
    </section>
  );
}
