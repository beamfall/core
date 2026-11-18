import { Copy, Link2, MonitorSmartphone, Plus } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { QrCode } from "@/components/beamfall/QrCode";
import {
  DataTable,
  EmptyState,
  ErrorState,
  HelpTip,
  StatusChip,
} from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { TableCell, TableRow } from "@/components/ui/table";
import { Advanced } from "@/lib/advancedSettings";
import {
  fetchConsoleConfig,
  fetchDevices,
  fetchRemoteAccess,
  mintPairingCode,
  type PairingCode,
  revokeAllDevices,
  revokeDevice,
  setConsoleConfig,
  setDeviceRole,
} from "@/lib/graphql";
import type { DeviceInfo, RemoteEndpointInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { ConfirmButton } from "../ConfirmButton";
import { usePairingRequests, useRoleLabel } from "../lib/pairingRequests";
import { Section } from "../Section";
import { useAsync } from "../useAsync";
import { enrollQrTarget, isLoopbackHost } from "./enrollQr";

// Device administration (ADR-0081), Console half — the operator-facing surface
// over already-enrolled browsers/devices. Lists every device with its role and
// revoked state, marks THIS browser (never revocable here — that would lock the
// operator out), and offers per-device revoke + revoke-all behind confirms with
// risk copy. It mints viewer/manager pairing codes (the "add a device" path,
// redeemed at /enroll) and exposes the remote-enrollment toggle. Domain-agnostic
// throughout. The pairing code is a CREDENTIAL: shown once for the operator to
// copy, never persisted in component state beyond the session and never logged.

type DeviceRole = "viewer" | "manager";
type MintedCode = PairingCode & { role: DeviceRole };

// Whole seconds left until `expiresAt`, ticking once a second; never negative.
function secondsUntil(deadline: number): number {
  return Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
}

function useSecondsLeft(expiresAt: string): number {
  const deadline = Date.parse(expiresAt);
  const [seconds, setSeconds] = useState(() => secondsUntil(deadline));
  useEffect(() => {
    setSeconds(secondsUntil(deadline));
    const timer = window.setInterval(() => setSeconds(secondsUntil(deadline)), 1000);
    return () => window.clearInterval(timer);
  }, [deadline]);
  return Number.isNaN(deadline) ? 0 : seconds;
}

// The QR a phone camera opens the enroll page from (CORE-1614), with the same
// URL as text beside it. On loopback it waits for the server's LAN endpoints
// and, with none, says how to get a QR instead of encoding localhost.
function EnrollQr() {
  const t = useT();
  const { origin, hostname } = window.location;
  const loopback = isLoopbackHost(hostname);
  const [endpoints, setEndpoints] = useState<RemoteEndpointInfo[] | null | undefined>(
    loopback ? undefined : null,
  );
  useEffect(() => {
    if (!loopback) return;
    const controller = new AbortController();
    fetchRemoteAccess(controller.signal)
      .then((info) => setEndpoints(info.endpoints))
      .catch(() => {
        if (!controller.signal.aborted) setEndpoints(null);
      });
    return () => controller.abort();
  }, [loopback]);

  if (endpoints === undefined) return null;
  const target = enrollQrTarget(origin, hostname, import.meta.env.BASE_URL, endpoints);
  if (!target) {
    return (
      <p className="mt-3 text-[12px] leading-snug text-muted-foreground">{t("deviceQrNeedsLan")}</p>
    );
  }
  return (
    <figure className="mt-3 flex flex-col items-start gap-2">
      <figcaption className="text-[12px] leading-snug text-muted-foreground">
        {t("deviceQrScan")}
      </figcaption>
      <QrCode value={target} label={t("deviceQrLabel", { url: target })} className="size-40" />
      <code className="text-[13px] break-all">{target}</code>
    </figure>
  );
}

// The freshly minted pairing-code banner — shown once so the operator can copy
// it before navigating away (the server does not re-issue the same code). It
// says which role the code grants, how long it has left (codes last 10
// minutes) and where to enter it (CORE-1607). The copied link carries the code
// in the URL fragment, which browsers never send to the server; the address
// shown on screen leaves it out so it can be typed by hand. Where the Clipboard
// API is missing (a plain-HTTP LAN address) or refuses, Copy link reveals the
// whole link in a read-only field with its text selected, to copy by hand.
function PairingCodeBanner({ code }: { code: MintedCode }) {
  const t = useT();
  const seconds = useSecondsLeft(code.expiresAt);
  const enrollURL = `${window.location.origin}${import.meta.env.BASE_URL}enroll`;
  const enrollLink = `${enrollURL}#code=${encodeURIComponent(code.code)}`;
  const roleLabel = t(code.role === "manager" ? "deviceRoleManager" : "deviceRoleViewer");
  const [manualLink, setManualLink] = useState(false);
  const linkFieldRef = useRef<HTMLInputElement>(null);
  const linkHintID = useId();
  useEffect(() => {
    if (!manualLink) return;
    linkFieldRef.current?.focus();
    linkFieldRef.current?.select();
  }, [manualLink]);

  async function copyLink() {
    try {
      if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
      await navigator.clipboard.writeText(enrollLink);
      setManualLink(false);
    } catch {
      setManualLink(true);
      linkFieldRef.current?.select();
    }
  }

  if (seconds === 0) {
    return (
      <div className="console-secret">
        <p className="console-secret-lead">
          <MonitorSmartphone className="console-nav-icon" aria-hidden />
          {t("deviceCodeExpired")}
        </p>
      </div>
    );
  }
  const time = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
  return (
    <div className="console-secret">
      <p className="console-secret-lead">
        <MonitorSmartphone className="console-nav-icon" aria-hidden />
        {t("deviceCodeShownFor", { role: roleLabel })}
      </p>
      <div className="console-secret-row">
        <code className="console-secret-value console-secret-code">{code.code}</code>
        <Button variant="outline" onClick={() => void navigator.clipboard?.writeText(code.code)}>
          <Copy className="console-nav-icon" aria-hidden />
          {t("copy")}
        </Button>
      </div>
      <p className="mt-2 text-[12px] leading-snug text-muted-foreground" role="timer">
        {t("deviceCodeExpiresIn", { time })}
      </p>
      <p className="mt-3 text-[12px] leading-snug text-muted-foreground">{t("deviceCodeOpenAt")}</p>
      <div className="console-secret-row">
        <code className="console-secret-value text-[13px] tracking-normal break-all">
          {enrollURL}
        </code>
        <Button variant="outline" size="sm" onClick={() => void copyLink()}>
          <Link2 className="console-nav-icon" aria-hidden />
          {t("deviceCodeCopyLink")}
        </Button>
      </div>
      <EnrollQr />
      {manualLink && (
        <div className="mt-2">
          <Input
            ref={linkFieldRef}
            readOnly
            value={enrollLink}
            aria-label={t("deviceCodeLinkField")}
            aria-describedby={linkHintID}
            onFocus={(e) => e.currentTarget.select()}
            className="h-9 font-mono text-[13px]"
          />
          <p id={linkHintID} className="mt-1.5 text-[12px] leading-snug text-muted-foreground">
            {t("deviceCodeLinkCopyHint")}
          </p>
        </div>
      )}
    </div>
  );
}

function ageLabel(t: ReturnType<typeof useT>, requestedAt: string): string {
  const seconds = Math.max(0, Math.round((Date.now() - Date.parse(requestedAt)) / 1000));
  if (Number.isNaN(seconds)) return "";
  return seconds < 60
    ? t("pairingAgeSeconds", { seconds })
    : t("pairingAgeMinutes", { minutes: Math.floor(seconds / 60) });
}

// Devices that asked to join (CORE-0317). The same list feeds the any-screen
// prompt (CORE-2064). A `code` in the page's query string (the QR a device
// shows) only shades that row so it can be found. Anyone can send an admin a
// link carrying their own code, so the link earns the request no order, no
// focus and no wording: the code on the device is what the admin checks.
// Approve grants Viewer; manager is a separate button, offered only to a
// device that asked for it. Names and platforms come from the devices
// themselves: text only.
function PendingRequests() {
  const t = useT();
  const roleLabel = useRoleLabel();
  const { requests, decide } = usePairingRequests();
  const [failed, setFailed] = useState<string | null>(null);
  const linked = new URLSearchParams(window.location.search).get("code");

  async function onDecide(decision: "approve" | "deny", code: string, grant?: "manager") {
    setFailed((await decide(decision, code, grant)) ? null : code);
  }

  return (
    <Section heading="pairingPendingHeading" lead="pairingPendingLead">
      {requests.length === 0 ? (
        <EmptyState message="pairingPendingEmpty" />
      ) : (
        <DataTable
          columns={[
            { key: "name", label: t("deviceColName") },
            { key: "platform", label: t("pairingColPlatform") },
            { key: "role", label: t("deviceColRole") },
            { key: "code", label: t("pairingColCode") },
            { key: "net", label: t("pairingColNetwork") },
            { key: "age", label: t("pairingColAge") },
            { key: "action", numeric: true },
          ]}
        >
          {requests.map((r) => (
            <TableRow
              key={r.code}
              data-linked={r.code === linked ? "true" : undefined}
              className={r.code === linked ? "bg-foreground/[0.04]" : undefined}
            >
              <TableCell className="font-medium">{r.deviceName}</TableCell>
              <TableCell>{r.platform}</TableCell>
              <TableCell>{roleLabel(r.requestedRole)}</TableCell>
              <TableCell>
                <code>{r.code}</code>
              </TableCell>
              <TableCell>{t(r.lan ? "pairingNetworkLan" : "pairingNetworkRemote")}</TableCell>
              <TableCell>{ageLabel(t, r.requestedAt)}</TableCell>
              <TableCell className="text-right">
                <div className="flex justify-end gap-2">
                  {failed === r.code && (
                    <span role="status" className="self-center text-[12px] text-destructive">
                      {t("pairingFailed")}
                    </span>
                  )}
                  <Button size="sm" onClick={() => void onDecide("approve", r.code)}>
                    {t(r.requestedRole === "manager" ? "pairingApproveViewer" : "pairingApprove")}
                  </Button>
                  {r.requestedRole === "manager" && (
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => void onDecide("approve", r.code, "manager")}
                    >
                      {t("pairingApproveManager")}
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" onClick={() => void onDecide("deny", r.code)}>
                    {t("pairingDeny")}
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          ))}
        </DataTable>
      )}
    </Section>
  );
}

// One device table. The ID tail tells apart devices that share a name such as
// "Chrome on macOS" (CORE-2324); the server does not yet report when a device
// was enrolled or last seen.
function DeviceTable({
  devices,
  onRevoke,
  onSetRole,
}: {
  devices: DeviceInfo[];
  onRevoke: (deviceID: string) => Promise<void>;
  onSetRole: (deviceID: string, role: "viewer" | "manager") => Promise<void>;
}) {
  const t = useT();
  return (
    <DataTable
      columns={[
        { key: "name", label: t("deviceColName") },
        { key: "role", label: t("deviceColRole") },
        { key: "state", label: t("deviceColState") },
        { key: "action", numeric: true },
      ]}
    >
      {devices.map((d) => (
        <TableRow key={d.deviceID}>
          <TableCell className="font-medium">
            {d.name}
            {d.current && <StatusChip tone="green">{t("deviceThisDevice")}</StatusChip>}
            <span className="block font-mono text-[12px] font-normal text-muted-foreground">
              {t("deviceIDDetail", { id: d.deviceID.slice(-6) })}
            </span>
          </TableCell>
          <TableCell>
            {t(d.role === "manager" ? "deviceRoleManager" : "deviceRoleViewer")}
          </TableCell>
          <TableCell>
            {d.revoked ? (
              <StatusChip tone="red">{t("deviceRevoked")}</StatusChip>
            ) : d.signedIn === false ? (
              <StatusChip>{t("deviceSignedOut")}</StatusChip>
            ) : (
              <StatusChip tone="green">{t("deviceActive")}</StatusChip>
            )}
          </TableCell>
          <TableCell className="text-right">
            {!d.current && !d.revoked && (
              <div className="flex justify-end gap-2">
                <ConfirmButton
                  label={t(d.role === "manager" ? "deviceDemote" : "devicePromote")}
                  confirm={d.role === "manager" ? "deviceDemoteConfirm" : "devicePromoteConfirm"}
                  size="sm"
                  variant="outline"
                  onConfirm={() =>
                    onSetRole(d.deviceID, d.role === "manager" ? "viewer" : "manager")
                  }
                />
                <ConfirmButton
                  label={t("deviceRevoke")}
                  confirm="deviceRevokeConfirm"
                  size="sm"
                  onConfirm={() => onRevoke(d.deviceID)}
                />
              </div>
            )}
          </TableCell>
        </TableRow>
      ))}
    </DataTable>
  );
}

export function Devices() {
  const t = useT();
  const devices = useAsync(fetchDevices);
  const config = useAsync(fetchConsoleConfig);
  const [minted, setMinted] = useState<MintedCode | null>(null);
  const [busy, setBusy] = useState(false);

  async function mint(role: DeviceRole) {
    setBusy(true);
    try {
      setMinted({ ...(await mintPairingCode(role)), role });
    } finally {
      setBusy(false);
    }
  }

  async function onRevoke(deviceID: string) {
    await revokeDevice(deviceID);
    await devices.refresh();
  }

  async function onRevokeAll() {
    await revokeAllDevices();
    await devices.refresh();
  }

  async function onSetRole(deviceID: string, role: "viewer" | "manager") {
    await setDeviceRole(deviceID, role);
    await devices.refresh();
  }

  async function onToggleRemote(enabled: boolean) {
    const next = await setConsoleConfig({ remoteEnrollmentEnabled: enabled });
    config.setData(next);
  }

  const list = devices.data ?? [];
  const revocableCount = list.filter((d) => !d.current && !d.revoked).length;
  // Revoked devices stay listed for the record but folded away, so the live
  // list is the devices that can still sign in (CORE-2324).
  const active = list.filter((d) => !d.revoked);
  const revoked = list.filter((d) => d.revoked);

  return (
    <div className="space-y-6">
      <PendingRequests />
      <Section
        heading="devicesHeading"
        lead="devicesLead"
        actions={
          revocableCount > 0 ? (
            <ConfirmButton
              label={t("deviceRevokeAll")}
              confirm="deviceRevokeAllConfirm"
              onConfirm={onRevokeAll}
            />
          ) : undefined
        }
      >
        {devices.loading ? (
          <Skeleton className="h-40 w-full" />
        ) : devices.failed ? (
          <ErrorState message="devicesUnavailable" />
        ) : list.length === 0 ? (
          <EmptyState message="devicesEmpty" />
        ) : (
          <>
            <DeviceTable devices={active} onRevoke={onRevoke} onSetRole={onSetRole} />
            {revoked.length > 0 && (
              <details className="mt-3">
                <summary className="cursor-pointer text-dense text-muted-foreground">
                  {t("deviceRevokedGroup", { count: revoked.length })}
                </summary>
                <div className="mt-2">
                  <DeviceTable devices={revoked} onRevoke={onRevoke} onSetRole={onSetRole} />
                </div>
              </details>
            )}
          </>
        )}
      </Section>

      <Section heading="deviceAddHeading" lead="deviceAddLead">
        {minted && <PairingCodeBanner key={minted.code} code={minted} />}
        <div
          className="mt-3 flex flex-wrap items-center gap-2"
          data-setting="console.access.pairingCodes"
        >
          <Button variant="outline" disabled={busy} onClick={() => void mint("viewer")}>
            <Plus className="console-nav-icon" aria-hidden />
            {t("deviceMintViewer")}
          </Button>
          <ConfirmButton
            label={t("deviceMintManager")}
            confirm="deviceMintManagerConfirm"
            variant="outline"
            onConfirm={() => mint("manager")}
          />
          <HelpTip label={t("accessHelpPairingCodesLabel")} text="accessHelpPairingCodesTip" />
        </div>
        <p className="mt-2 text-[12px] leading-snug text-muted-foreground">{t("deviceMintHint")}</p>
      </Section>

      {/* Remote enrollment is an Advanced setting, kept in view while on (CORE-1117). */}
      <Advanced keep={Boolean(config.data?.remoteEnrollmentEnabled)}>
        <Section heading="remoteEnrollHeading" lead="remoteEnrollLead" collapsible>
          {config.loading ? (
            <Skeleton className="h-10 w-full" />
          ) : config.failed || !config.data ? (
            <ErrorState message="devicesUnavailable" />
          ) : (
            <div
              className="flex items-start justify-between gap-4 console-card console-card-pad"
              data-setting="console.access.remoteEnrollment"
            >
              <div className="space-y-1">
                <span className="bf-label-with-tip">
                  <p className="m-0 text-dense font-semibold">{t("remoteEnrollToggle")}</p>
                  <HelpTip label={t("remoteEnrollToggle")} text="accessHelpRemoteEnrollTip" />
                </span>
                <p className="m-0 text-[12px] leading-snug text-muted-foreground">
                  {t("remoteEnrollToggleHint")}
                </p>
                <Link to="/console/remote" className="text-[12px] underline underline-offset-2">
                  {t("accessHelpRemoteEnrollLink")}
                </Link>
              </div>
              <Switch
                checked={config.data.remoteEnrollmentEnabled}
                onCheckedChange={(v) => void onToggleRemote(v)}
                aria-label={t("remoteEnrollToggle")}
              />
            </div>
          )}
        </Section>
      </Advanced>
    </div>
  );
}
