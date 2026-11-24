import { type FormEvent, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { clearDevicePlayerBinding, setDevicePlayerBinding } from "@/lib/graphql";
import type { DeviceInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { getAdminEpochSignal } from "../adminEpoch";
import { ConfirmButton } from "../ConfirmButton";

export interface PlayerProfileChoice {
  id: string;
  name: string;
}

/** The "Profile name · display name" line under a bound device's name. */
export function devicePlayerBoundText(
  device: DeviceInfo,
  players: PlayerProfileChoice[],
  t: ReturnType<typeof useT>,
): string | null {
  if (!device.playerProfileID) return null;
  const profile = players.find((p) => p.id === device.playerProfileID)?.name ?? "";
  return t("consoleDevicePlayerBound", {
    name: device.playerDisplayName || device.name,
    profile,
  });
}

// Binds a Device to a Player Profile with a display name, or clears the
// binding (ADR-0240 D2). The server refuses a revoked device and a non-player
// Profile; any failure shows one localized line.
export function DevicePlayerBinding({
  device,
  players,
  onChanged,
}: {
  device: DeviceInfo;
  players: PlayerProfileChoice[];
  onChanged: () => Promise<void> | void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [profileID, setProfileID] = useState(device.playerProfileID ?? players[0]?.id ?? "");
  const [name, setName] = useState(device.playerDisplayName || device.name);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!profileID) return;
    // Rebinding ends the device's sessions and playback grants, so a save that
    // changes nothing just closes the dialog.
    const shown = device.playerDisplayName || device.name;
    if (profileID === device.playerProfileID && (name.trim() || shown) === shown) {
      setOpen(false);
      return;
    }
    setBusy(true);
    setFailed(false);
    try {
      await setDevicePlayerBinding(
        device.deviceID,
        profileID,
        name.trim() || undefined,
        getAdminEpochSignal(),
      );
      setOpen(false);
      await onChanged();
    } catch {
      setFailed(true);
    } finally {
      setBusy(false);
    }
  }

  const title = t("consoleDevicePlayerBindTitle", { device: device.name });
  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(next) => {
          if (busy) return;
          if (next) {
            setProfileID(device.playerProfileID ?? players[0]?.id ?? "");
            setName(device.playerDisplayName || device.name);
            setFailed(false);
          }
          setOpen(next);
        }}
      >
        <DialogTrigger asChild>
          <Button type="button" size="sm" variant="outline" aria-label={title}>
            {t(device.playerProfileID ? "consoleDevicePlayerHeading" : "consoleDevicePlayerBind")}
          </Button>
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{t("consoleDevicePlayerBindLead")}</DialogDescription>
          </DialogHeader>
          {players.length === 0 ? (
            <p className="m-0 text-sm">{t("consoleDevicePlayerNoProfiles")}</p>
          ) : (
            <form id={`bind-${device.deviceID}`} onSubmit={submit} className="space-y-4">
              <div className="space-y-1">
                <Label>{t("consoleDevicePlayerProfileLabel")}</Label>
                <Select value={profileID} onValueChange={setProfileID}>
                  <SelectTrigger aria-label={t("consoleDevicePlayerProfileLabel")}>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {players.map((p) => (
                      <SelectItem key={p.id} value={p.id}>
                        {p.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label htmlFor={`bind-name-${device.deviceID}`}>
                  {t("consoleDevicePlayerNameLabel")}
                </Label>
                <Input
                  id={`bind-name-${device.deviceID}`}
                  value={name}
                  maxLength={64}
                  autoComplete="off"
                  onChange={(e) => setName(e.target.value)}
                />
              </div>
              {failed && (
                <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
                  {t("consoleDevicePlayerFailed")}
                </p>
              )}
            </form>
          )}
          {players.length > 0 && (
            <DialogFooter>
              <Button type="submit" form={`bind-${device.deviceID}`} disabled={busy || !profileID}>
                {t("consoleDevicePlayerSave")}
              </Button>
            </DialogFooter>
          )}
        </DialogContent>
      </Dialog>
      {device.playerProfileID && (
        <ConfirmButton
          label={t("consoleDevicePlayerClear")}
          confirm="consoleDevicePlayerClearConfirm"
          size="sm"
          variant="outline"
          onConfirm={async () => {
            await clearDevicePlayerBinding(device.deviceID, getAdminEpochSignal());
            await onChanged();
          }}
        >
          {t("consoleDevicePlayerClearEffect")}
        </ConfirmButton>
      )}
    </>
  );
}
