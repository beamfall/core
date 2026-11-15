import { Users } from "lucide-react";
import { type FormEvent, useEffect, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n";
import { formatClockTime } from "../lib/transport";
import type { WatchPartyPlayback } from "./useWatchParty";
import {
  clearWatchPartyNotice,
  decidePartyRequest,
  endParty,
  inviteToParty,
  useWatchPartyState,
  type WatchPartyRequest,
  type WatchPartyState,
} from "./watchPartyStore";
import { driftLevel } from "./watchPartySync";

// The player bar's Watch Party control (CORE-1677): start a party from the
// title playing, invite a household Profile by name, see who is in it, leave.
// A host can also end it for everyone (CORE-1763), chooses at start whether
// everyone or only the host controls playback, and decides members' requests
// in a host-only party (CORE-1765).

/** How long a hand-off or end notice stays on the stage. */
const NOTICE_MS = 6000;

export function WatchPartyPanel({
  party,
  open,
  onOpenChange,
  onStart,
  onLeave,
}: {
  party: WatchPartyState;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onStart: (relaxed: boolean) => Promise<boolean>;
  onLeave: () => Promise<void>;
}) {
  const t = useT();
  const panelID = useId();
  const inputID = useId();
  const [name, setName] = useState("");
  const [inviting, setInviting] = useState(false);
  const [relaxed, setRelaxed] = useState(true);
  const active = party.status === "active";
  const busy = party.status === "starting" || party.status === "joining";

  async function invite(event: FormEvent) {
    event.preventDefault();
    if (!name.trim() || inviting) return;
    setInviting(true);
    if (await inviteToParty(name)) setName("");
    setInviting(false);
  }

  const error =
    party.error === "home-network"
      ? t("watchPartyHomeNetworkOnly")
      : party.error === "failed"
        ? t("watchPartyFailed", { reason: party.errorDetail })
        : "";

  return (
    <div className="relative ml-auto">
      <Button
        variant={active ? "default" : "outline"}
        size="sm"
        aria-expanded={open}
        aria-controls={panelID}
        onClick={() => onOpenChange(!open)}
      >
        <Users aria-hidden className="size-4" />
        {t("watchPartyButton")}
        {active && party.members.length > 0 ? ` · ${party.members.length}` : ""}
      </Button>
      {open && (
        <section
          id={panelID}
          aria-label={t("watchPartyButton")}
          className="absolute right-0 top-full mt-2 flex w-[min(20rem,calc(100vw-2rem))] flex-col gap-3 rounded-lg border border-white/15 bg-[oklch(14%_0.012_255)] p-4 text-sm text-white shadow-lg"
        >
          {!active ? (
            <>
              <p className="m-0 text-white/75">{t("watchPartyIntro")}</p>
              <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
                <legend className="mb-1 p-0 text-white/75">{t("watchPartyControl")}</legend>
                {[
                  { value: true, label: t("watchPartyRelaxed") },
                  { value: false, label: t("watchPartyHostOnly") },
                ].map((option) => (
                  <label key={String(option.value)} className="flex items-center gap-2">
                    <input
                      type="radio"
                      name={`${panelID}-control`}
                      checked={relaxed === option.value}
                      disabled={busy}
                      onChange={() => setRelaxed(option.value)}
                    />
                    {option.label}
                  </label>
                ))}
              </fieldset>
              <Button size="sm" disabled={busy} onClick={() => void onStart(relaxed)}>
                {busy ? t("watchPartyStarting") : t("watchPartyStart")}
              </Button>
            </>
          ) : (
            <>
              <div>
                <h2 className="m-0 mb-1 text-xs font-medium uppercase tracking-wide text-white/60">
                  {t("watchPartyMembers")}
                </h2>
                <ul className="m-0 flex list-none flex-col gap-1 p-0">
                  {party.members.map((member) => (
                    <li key={member}>{member}</li>
                  ))}
                  {party.invited.map((member) => (
                    <li key={`invited-${member}`} className="text-white/60">
                      {t("watchPartyInvited", { name: member })}
                    </li>
                  ))}
                </ul>
              </div>
              {party.isHost && party.requests.length > 0 && (
                <PartyRequests requests={party.requests} />
              )}
              <form className="flex flex-col gap-1" onSubmit={(event) => void invite(event)}>
                <label htmlFor={inputID} className="text-white/75">
                  {t("watchPartyInviteLabel")}
                </label>
                <div className="flex gap-2">
                  <Input
                    id={inputID}
                    value={name}
                    placeholder={t("watchPartyInvitePlaceholder")}
                    autoComplete="off"
                    onChange={(event) => setName(event.target.value)}
                  />
                  <Button type="submit" size="sm" disabled={!name.trim() || inviting}>
                    {t("watchPartyInvite")}
                  </Button>
                </div>
              </form>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={() => void onLeave()}>
                  {t("watchPartyLeave")}
                </Button>
                {party.isHost && (
                  <Button variant="outline" size="sm" onClick={() => void endParty()}>
                    {t("watchPartyEnd")}
                  </Button>
                )}
              </div>
            </>
          )}
          {error && (
            <p className="m-0 text-[var(--state-danger)]" role="alert">
              {error}
            </p>
          )}
        </section>
      )}
    </div>
  );
}

/** The host's pending member requests, each with Approve and Decline. */
function PartyRequests({ requests }: { requests: WatchPartyRequest[] }) {
  const t = useT();
  return (
    <div>
      <h2 className="m-0 mb-1 text-xs font-medium uppercase tracking-wide text-white/60">
        {t("watchPartyRequests")}
      </h2>
      <ul className="m-0 flex list-none flex-col gap-2 p-0">
        {requests.map((request) => {
          const vars = { name: request.from, position: formatClockTime(request.positionMS / 1000) };
          return (
            <li key={request.id} className="flex flex-col gap-1" data-party-request={request.id}>
              <span>
                {request.paused
                  ? t("watchPartyRequestPause", vars)
                  : t("watchPartyRequestPlay", vars)}
              </span>
              <div className="flex gap-2">
                <Button size="sm" onClick={() => void decidePartyRequest(request.id, true)}>
                  {t("watchPartyApprove")}
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void decidePartyRequest(request.id, false)}
                >
                  {t("watchPartyDecline")}
                </Button>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

/** Formats a drift as a signed duration, for example "+120 ms" or "−1.2 s". */
export function formatDrift(driftMS: number): string {
  const sign = driftMS < 0 ? "−" : "+";
  const magnitude = Math.abs(driftMS);
  return magnitude >= 1000
    ? `${sign}${(magnitude / 1000).toFixed(1)} s`
    : `${sign}${Math.round(magnitude)} ms`;
}

/** The stage's sync indicator: how far this player is from the party timeline. */
export function WatchPartySyncIndicator({
  playback,
  driftMS,
  needsTap,
  onResume,
}: {
  playback: WatchPartyPlayback;
  driftMS: number | null;
  needsTap: boolean;
  onResume: () => void;
}) {
  const t = useT();
  const party = useWatchPartyState();
  useEffect(() => {
    if (!party.notice) return;
    const timer = setTimeout(clearWatchPartyNotice, NOTICE_MS);
    return () => clearTimeout(timer);
  }, [party.notice]);
  if (playback !== "off" && needsTap) {
    return (
      <div className="absolute right-4 top-16 z-20">
        <Button size="sm" onClick={onResume}>
          {t("watchPartyTapToJoin")}
        </Button>
      </div>
    );
  }
  if (party.notice) {
    const name = party.noticeName;
    const label =
      party.notice === "ended"
        ? t("watchPartyEnded", { name })
        : party.isHost
          ? t("watchPartyYouHost")
          : t("watchPartyHandedOff", { name });
    return (
      <p
        className="pointer-events-none absolute right-4 top-16 z-20 m-0 rounded-md bg-black/70 px-3 py-1 text-sm text-white"
        role="status"
        data-party-notice={party.notice}
      >
        {label}
      </p>
    );
  }
  if (playback === "off") return null;
  let label: string;
  let level = "in-sync";
  if (playback === "waiting") label = t("watchPartyWaiting");
  else if (playback === "paused") label = t("watchPartyPausedForAll");
  else {
    level = driftLevel(driftMS ?? 0);
    const drift = formatDrift(driftMS ?? 0);
    label =
      level === "in-sync"
        ? t("watchPartyInSync")
        : level === "adjusting"
          ? t("watchPartyAdjusting", { drift })
          : t("watchPartyResyncing", { drift });
  }
  const dot =
    level === "in-sync"
      ? "bg-[var(--state-direct)]"
      : level === "adjusting"
        ? "bg-[var(--state-warning)]"
        : "bg-[var(--state-danger)]";
  return (
    <p
      className="pointer-events-none absolute right-4 top-16 z-20 m-0 flex items-center gap-2 rounded-md bg-black/70 px-3 py-1 text-sm text-white"
      role="status"
      data-drift-level={level}
    >
      <span className="sr-only">{t("watchPartySyncStatus")}: </span>
      <span aria-hidden className={`inline-block size-2 rounded-full ${dot}`} />
      {label}
    </p>
  );
}
