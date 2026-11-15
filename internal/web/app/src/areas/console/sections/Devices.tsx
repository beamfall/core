import { Copy, Link2, MonitorSmartphone, Plus } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { DataTable, EmptyState, ErrorState, StatusChip } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { TableCell, TableRow } from "@/components/ui/table";
import { Advanced } from "@/lib/advancedSettings";
import {
  fetchConsoleConfig,
  fetchDevices,
  mintPairingCode,
  type PairingCode,
  revokeAllDevices,
  revokeDevice,
  setConsoleConfig,
  setDeviceRole,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

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
        <code className="console-secret-value">{code.code}</code>
        <Button
          variant="outline"
          size="sm"
          onClick={() => void navigator.clipboard?.writeText(code.code)}
        >
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

  return (
    <div className="space-y-6">
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
          <DataTable
            columns={[
              { key: "name", label: t("deviceColName") },
              { key: "role", label: t("deviceColRole") },
              { key: "state", label: t("deviceColState") },
              { key: "action", numeric: true },
            ]}
          >
            {list.map((d) => (
              <TableRow key={d.deviceID}>
                <TableCell className="font-medium">
                  {d.name}
                  {d.current && <StatusChip tone="green">{t("deviceThisDevice")}</StatusChip>}
                </TableCell>
                <TableCell>
                  {t(d.role === "manager" ? "deviceRoleManager" : "deviceRoleViewer")}
                </TableCell>
                <TableCell>
                  {d.revoked ? (
                    <StatusChip tone="red">{t("deviceRevoked")}</StatusChip>
                  ) : (
                    <StatusChip tone="green">{t("deviceActive")}</StatusChip>
                  )}
                </TableCell>
                <TableCell className="text-right">
                  {!d.current && !d.revoked && (
                    <div className="flex justify-end gap-2">
                      <ConfirmButton
                        label={t(d.role === "manager" ? "deviceDemote" : "devicePromote")}
                        confirm={
                          d.role === "manager" ? "deviceDemoteConfirm" : "devicePromoteConfirm"
                        }
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
        )}
      </Section>

      <Section heading="deviceAddHeading" lead="deviceAddLead">
        {minted && <PairingCodeBanner key={minted.code} code={minted} />}
        <div className="mt-3 flex flex-wrap items-center gap-2">
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
            <div className="flex items-start justify-between gap-4 console-card console-card-pad">
              <div className="space-y-1">
                <p className="m-0 text-dense font-semibold">{t("remoteEnrollToggle")}</p>
                <p className="m-0 text-[12px] leading-snug text-muted-foreground">
                  {t("remoteEnrollToggleHint")}
                </p>
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
