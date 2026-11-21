import { type FormEvent, useId, useRef, useState } from "react";
import { EmptyLine, ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { ProfileSettings } from "./ProfileSettings";

// Profile picker (ADR-0004, ported from index.html profile-view): switch the
// session's bound Profile. Managed Profiles carry a quiet hint (they have no
// reveal path — the hint explains its absence). The current binding is marked
// and never re-switched. Switching always relocks server-side; the session
// spine reloads everything on success.
export function ProfilePage({
  onSwitch,
  error,
}: {
  onSwitch: (id: string, pin?: string) => Promise<void>;
  error: string;
}) {
  const t = useT();
  const { profiles, session, isManager } = useSession();
  const [pinProfileID, setPinProfileID] = useState("");
  const [busy, setBusy] = useState(false);
  const [savingPin, setSavingPin] = useState(false);

  const currentID = session?.profileID ?? "";
  const pinProfile = profiles.find((profile) => profile.id === pinProfileID);

  async function switchTo(id: string, pin?: string) {
    setPinProfileID("");
    setBusy(true);
    try {
      await onSwitch(id, pin);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-5">
      <ManageSection>
        {profiles.length === 0 ? (
          <EmptyLine message={t("profileUnavailable")} />
        ) : (
          <ul className="space-y-1.5">
            {profiles.map((profile) => {
              const current = profile.id === currentID;
              return (
                <li key={profile.id}>
                  <Button
                    type="button"
                    variant={current ? "secondary" : "outline"}
                    size="sm"
                    className="w-full justify-start gap-2"
                    disabled={busy || savingPin}
                    aria-current={current ? "true" : undefined}
                    onClick={() => {
                      if (current) return;
                      if (profile.kind === "adult" && profile.entryPinSet === true) {
                        setPinProfileID(profile.id);
                      } else {
                        void switchTo(profile.id);
                      }
                    }}
                  >
                    <span className="font-semibold">{profile.name}</span>
                    {profile.kind === "managed" && (
                      <StatusBadge state="restricted">{t("managed")}</StatusBadge>
                    )}
                    {current && <span className="ml-auto text-xs text-[var(--accent)]">●</span>}
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
        <ErrorLine message={error} />
      </ManageSection>
      {isManager && session?.profile === "adult" && (
        <ProfileSettings savingPin={savingPin} setSavingPin={setSavingPin} />
      )}
      {pinProfile && (
        <ProfileSwitchPin
          name={pinProfile.name}
          onClose={() => setPinProfileID("")}
          onSubmit={(pin) => void switchTo(pinProfile.id, pin)}
        />
      )}
    </div>
  );
}

function ProfileSwitchPin({
  name,
  onClose,
  onSubmit,
}: {
  name: string;
  onClose: () => void;
  onSubmit: (pin: string) => void;
}) {
  const t = useT();
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [pin, setPin] = useState("");

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = pin.trim();
    if (value) onSubmit(value);
  }

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent initialFocus={input} showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{t("whosWatchingPinTitle", { name })}</DialogTitle>
          <DialogDescription>{t("whosWatchingPinLead")}</DialogDescription>
        </DialogHeader>
        <form className="space-y-4" onSubmit={submit}>
          <div className="space-y-1">
            <Label htmlFor={id}>{t("pin")}</Label>
            <Input
              ref={input}
              id={id}
              type="password"
              inputMode="numeric"
              autoComplete="off"
              value={pin}
              onChange={(event) => setPin(event.target.value)}
            />
          </div>
          <DialogFooter>
            <DialogClose render={<Button type="button" variant="outline" />}>
              {t("cancel")}
            </DialogClose>
            <Button type="submit" disabled={!pin.trim()}>
              {t("whosWatchingContinue")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
