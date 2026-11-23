import { ExternalLink, MonitorUp, QrCode as QrIcon, RefreshCw } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useHref } from "react-router";
import { QrCode } from "@/components/beamfall/QrCode";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import {
  fetchHomeLibraries,
  type HomeLibrary,
  type PartyFairness,
  type PartySession,
  partyEnd,
  partyRotateCode,
  partyStart,
  partyUpdate,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { ViewerActions } from "../components/ViewerActions";
import {
  PartyFind,
  PartyMonitor,
  PartyPeople,
  PartyRotation,
  PartyScreens,
} from "../party/HostLive";
import { stagePath, stageWindowName } from "../party/partySync";
import { type RunPartyOp, usePartyHost } from "../party/usePartyHost";
import "../party/party.css";

export type { RunPartyOp } from "../party/usePartyHost";

/**
 * The party host page (CORE-2945): start a session over chosen Libraries, then
 * run it: the live monitor and transport, the rotation, guest requests, adding
 * songs for singers, the join code and QR, and the stage screens.
 */
export function PartyHostPage() {
  const t = useT();
  const { session, loadError, notice, ranges, serverNow, run } = usePartyHost();

  return (
    <AppShell actions={<ViewerActions />}>
      <div className="bf-party mx-auto flex max-w-7xl flex-col gap-6">
        {session ? (
          <PartyHeader session={session} run={run} />
        ) : (
          <header className="flex flex-col gap-1.5">
            <h1 className="bf-page-title">{t("partyHostTitle")}</h1>
            <p className="bf-lead max-w-2xl">{t("partyHostLead")}</p>
          </header>
        )}
        {notice && (
          <p
            role={notice.tone === "error" ? "alert" : "status"}
            className="bf-party-notice"
            data-tone={notice.tone}
          >
            {notice.text}
          </p>
        )}
        {loadError && session === undefined ? (
          <p role="alert" className="bf-party-notice" data-tone="error">
            {t("partyHostLoadError")}
          </p>
        ) : session === undefined ? null : session === null ? (
          <PartyStartForm run={run} />
        ) : (
          <>
            <div className="bf-party-band">
              <PartyMonitor session={session} run={run} ranges={ranges} serverNow={serverNow} />
              <PartyScreens session={session} run={run} />
            </div>
            <div className="bf-party-lower">
              <PartyFind session={session} run={run} />
              <PartyRotation session={session} run={run} />
            </div>
            <PartyPeople session={session} run={run} />
            <PartySettings session={session} run={run} />
          </>
        )}
      </div>
    </AppShell>
  );
}

function PartyHeader({ session, run }: { session: PartySession; run: RunPartyOp }) {
  const t = useT();
  const [showQR, setShowQR] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const stageHref = useHref(stagePath(session.id));
  const joinURL = `${window.location.origin}${session.joinPath}`;

  const popOut = () => {
    const opened = window.open(
      stageHref,
      stageWindowName(session.id),
      "popup,width=1280,height=720",
    );
    setBlocked(!opened);
  };

  return (
    <header className="bf-party-header">
      <div className="flex min-w-0 flex-col gap-1">
        <h1 className="bf-page-title">{t("partyHostTitle")}</h1>
        <p className="bf-party-muted">
          {t("partyHostJoinCode")} <strong className="bf-party-code">{session.joinCode}</strong>
          {!session.joinOpen && <span> · {t("partyHostJoinClosed")}</span>}
        </p>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          aria-expanded={showQR}
          onClick={() => setShowQR((value) => !value)}
        >
          <QrIcon className="size-3.5" aria-hidden />
          {showQR ? t("partyHostHideQR") : t("partyHostShowQR")}
        </Button>
        <Button variant="outline" size="sm" onClick={() => void run(partyRotateCode)}>
          <RefreshCw className="size-3.5" aria-hidden />
          {t("partyHostRotateCode")}
        </Button>
        <Button size="sm" onClick={popOut}>
          <MonitorUp className="size-3.5" aria-hidden />
          {t("partyHostPopOut")}
        </Button>
      </div>
      {blocked && (
        <p role="alert" className="bf-party-notice w-full" data-tone="error">
          {t("partyHostPopupBlocked")}{" "}
          <a href={stageHref} target="_blank" rel="noopener" className="bf-party-link">
            {t("partyHostOpenStage")}
            <ExternalLink className="ml-1 inline size-3.5" aria-hidden />
          </a>
        </p>
      )}
      {showQR && (
        <div className="bf-party-qr w-full">
          <QrCode
            value={joinURL}
            label={t("partyHostQRLabel", { code: session.joinCode })}
            className="size-40"
          />
          <div className="flex flex-col gap-1">
            <p>{t("partyHostQRHint")}</p>
            <p className="bf-party-muted break-all">{joinURL}</p>
          </div>
        </div>
      )}
    </header>
  );
}

function PartyStartForm({ run }: { run: RunPartyOp }) {
  const t = useT();
  const id = useId();
  const [libraries, setLibraries] = useState<HomeLibrary[] | null | undefined>(undefined);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [fairness, setFairness] = useState<PartyFairness>("round-robin");
  const [autoAdd, setAutoAdd] = useState(false);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    fetchHomeLibraries(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setLibraries(next);
      })
      .catch(() => {
        if (!controller.signal.aborted) setLibraries(null);
      });
    return () => controller.abort();
  }, []);

  const toggle = (libraryID: string, on: boolean) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (on) next.add(libraryID);
      else next.delete(libraryID);
      return next;
    });

  const start = async () => {
    setBusy(true);
    await run(() => partyStart({ libraryIDs: [...picked], fairness, autoAdd }));
    setBusy(false);
  };

  return (
    <section className="bf-party-panel flex flex-col gap-5" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`} className="bf-party-h2">
        {t("partyHostStartTitle")}
      </h2>
      <fieldset className="flex flex-col gap-2">
        <legend className="bf-party-label">{t("partyHostLibraries")}</legend>
        {libraries === undefined ? null : libraries === null ? (
          <p className="bf-party-muted">{t("partyHostLibrariesUnavailable")}</p>
        ) : libraries.length === 0 ? (
          <p className="bf-party-muted">{t("partyHostLibrariesEmpty")}</p>
        ) : (
          libraries.map((library) => (
            <label key={library.id} className="flex items-center gap-2">
              <Checkbox
                checked={picked.has(library.id)}
                onCheckedChange={(value) => toggle(library.id, value === true)}
              />
              {library.title}
            </label>
          ))
        )}
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="bf-party-label">{t("partyHostFairness")}</legend>
        <FairnessChoice name={`${id}-fairness`} value={fairness} onChange={setFairness} />
      </fieldset>
      <label className="flex items-center gap-3">
        <Switch checked={autoAdd} onCheckedChange={setAutoAdd} />
        <span>
          {t("partyHostAutoAdd")}
          <span className="bf-party-muted block text-sm">{t("partyHostAutoAddHint")}</span>
        </span>
      </label>
      <div>
        <Button onClick={() => void start()} disabled={busy || picked.size === 0}>
          {t("partyHostStart")}
        </Button>
      </div>
    </section>
  );
}

function FairnessChoice({
  name,
  value,
  onChange,
}: {
  name: string;
  value: PartyFairness;
  onChange: (next: PartyFairness) => void;
}) {
  const t = useT();
  const options: [PartyFairness, string][] = [
    ["round-robin", t("partyHostFairnessRoundRobin")],
    ["fifo", t("partyHostFairnessFifo")],
  ];
  return (
    <>
      {options.map(([option, label]) => (
        <label key={option} className="flex items-center gap-2">
          <input
            type="radio"
            name={name}
            value={option}
            checked={value === option}
            onChange={() => onChange(option)}
            className="bf-party-radio"
          />
          {label}
        </label>
      ))}
    </>
  );
}

function PartySettings({ session, run }: { session: PartySession; run: RunPartyOp }) {
  const t = useT();
  const id = useId();
  const [confirmEnd, setConfirmEnd] = useState(false);
  return (
    <section className="bf-party-panel flex flex-col gap-4" aria-labelledby={`${id}-title`}>
      <h2 id={`${id}-title`} className="bf-party-h2">
        {t("partyHostSettings")}
      </h2>
      <label className="flex items-center gap-3">
        <Switch
          checked={session.joinOpen}
          onCheckedChange={(joinOpen) => void run(() => partyUpdate({ joinOpen }))}
        />
        {t("partyHostJoinOpen")}
      </label>
      <label className="flex items-center gap-3">
        <Switch
          checked={session.autoAdd}
          onCheckedChange={(autoAdd) => void run(() => partyUpdate({ autoAdd }))}
        />
        {t("partyHostAutoAdd")}
      </label>
      <fieldset className="flex flex-col gap-2">
        <legend className="bf-party-label">{t("partyHostFairness")}</legend>
        <FairnessChoice
          name={`${id}-fairness`}
          value={session.fairness}
          onChange={(fairness) => void run(() => partyUpdate({ fairness }))}
        />
      </fieldset>
      <div className="flex flex-wrap items-center gap-2">
        {confirmEnd ? (
          <>
            <span>{t("partyHostEndConfirm")}</span>
            <Button
              variant="destructive"
              size="sm"
              onClick={() => void run(async () => ((await partyEnd()) ? null : session))}
            >
              {t("partyHostEndYes")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setConfirmEnd(false)}>
              {t("partyHostCancel")}
            </Button>
          </>
        ) : (
          <Button variant="danger" size="sm" onClick={() => setConfirmEnd(true)}>
            {t("partyHostEnd")}
          </Button>
        )}
      </div>
    </section>
  );
}
