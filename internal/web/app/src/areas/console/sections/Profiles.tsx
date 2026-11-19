import { ChevronDown, ChevronUp, GripVertical } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useRef, useState } from "react";
import { AvatarChooser, ProfileAvatar, ProfilePhotoEditor } from "@/components/beamfall/profile";
import { EmptyState, ErrorState, HelpTip } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
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
import {
  createProfile,
  disableProfile,
  fetchProfilesAdmin,
  reorderProfiles,
  setProfileAvatar,
  updateProfile,
} from "@/lib/graphql";
import type { ProfileAdminInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { getAdminEpochSignal } from "../adminEpoch";
import { ConfirmButton } from "../ConfirmButton";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// The Console Profiles lane (ADR-0086 §46 — a distinct axis from Devices and
// Library policy): the household's Profile identity lifecycle. Lists Profiles,
// creates adult/managed Profiles, and offboards (disables) one with an explicit
// effect explanation behind a confirm dialog. Every mutation is Admin-Session-
// gated server-side. A Profile is not an Account (ADR-0004); no account or
// entitlement data is touched here.
//
// Disable is one-way in the web Console today: the server `profiles` query
// excludes disabled Profiles, so an offboarded Profile drops off the list and
// cannot yet be re-enabled here (server follow-up). The confirm copy is honest
// about this.
//
// The list order is the household's picker order on every device (CORE-2477):
// rows drag by their grip, and for the keyboard the move buttons and Alt+Up/
// Down on a focused row do the same, with a live region announcing the new
// position. Each row's avatar opens a dialog for its icon, colour and picture
// (CORE-1240, CORE-1241).
export function Profiles() {
  const t = useT();
  const profiles = useAsync(fetchProfilesAdmin);
  const rows = profiles.data ?? [];
  const [dragging, setDragging] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [reorderFailed, setReorderFailed] = useState(false);
  const saving = useRef(false);
  const pendingOrder = useRef<string[] | null>(null);
  // After a failed save the list reloads from the server; moves wait for it so
  // none is queued against an order that is about to be replaced.
  const [recovering, setRecovering] = useState(false);

  function refreshAfterChange() {
    // The reorder's final refresh also picks up changes made during the save.
    if (!saving.current) void profiles.refresh();
  }

  async function move(from: number, to: number) {
    if (recovering || from === to || from < 0 || to < 0 || to >= rows.length) return;
    const next = rows.slice();
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    profiles.mutate(next);
    setReorderFailed(false);
    setAnnouncement(
      t("consoleProfilesMoved", { name: moved.name, position: to + 1, total: next.length }),
    );
    pendingOrder.current = next.map((p) => p.id);
    if (saving.current) return;
    saving.current = true;
    const session = getAdminEpochSignal();
    let saved = false;
    try {
      // One save at a time, always of the newest order: concurrent requests
      // could commit out of order and leave the server on an older one.
      while (pendingOrder.current && !session?.aborted) {
        const order = pendingOrder.current;
        pendingOrder.current = null;
        await reorderProfiles(order);
      }
      saved = true;
    } catch {
      pendingOrder.current = null;
    } finally {
      saving.current = false;
    }
    if (session?.aborted) return;
    if (saved) {
      void profiles.refresh();
      return;
    }

    setReorderFailed(true);
    setRecovering(true);
    try {
      await profiles.refresh();
    } finally {
      setRecovering(false);
    }
  }

  if (profiles.failed) {
    return (
      <Section heading="consoleProfilesHeading" lead="accessHelpProfilesLead">
        <ErrorState message="consoleProfilesUnavailable" />
      </Section>
    );
  }

  return (
    <Section heading="consoleProfilesHeading" lead="accessHelpProfilesLead">
      <div className="space-y-6">
        <CreateProfileForm onCreated={refreshAfterChange} />

        {rows.length === 0 ? (
          <EmptyState message="consoleProfilesEmpty" />
        ) : (
          <div className="space-y-2">
            {rows.length > 1 && (
              <p className="m-0 text-sm text-muted-foreground">{t("consoleProfilesOrderLead")}</p>
            )}
            {reorderFailed && (
              <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
                {t("consoleProfilesReorderFailed")}
              </p>
            )}
            <ul className="console-card divide-y divide-border">
              {rows.map((p, index) => (
                <ProfileRow
                  key={p.id}
                  profile={p}
                  index={index}
                  count={rows.length}
                  locked={recovering}
                  dragging={dragging}
                  onDragging={setDragging}
                  onMove={(from, to) => void move(from, to)}
                  onDropHere={() => {
                    const from = rows.findIndex((r) => r.id === dragging);
                    setDragging(null);
                    void move(from, index);
                  }}
                  onChanged={refreshAfterChange}
                />
              ))}
            </ul>
          </div>
        )}
        <p aria-live="polite" className="sr-only">
          {announcement}
        </p>
      </div>
    </Section>
  );
}

function CreateProfileForm({ onCreated }: { onCreated: () => void }) {
  const t = useT();
  const [name, setName] = useState("");
  const [kind, setKind] = useState("adult");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    setBusy(true);
    setError(false);
    try {
      await createProfile(trimmed, kind);
      setName("");
      setKind("adult");
      onCreated();
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form
      onSubmit={submit}
      className="console-card console-card-pad flex flex-wrap items-end gap-3"
    >
      <div className="min-w-48 flex-1 space-y-1">
        <Label htmlFor="new-profile-name">{t("consoleProfilesNameLabel")}</Label>
        <Input
          id="new-profile-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoComplete="off"
        />
      </div>
      <div className="space-y-1" data-setting="console.access.profileType">
        <span className="bf-label-with-tip">
          <Label>{t("accessHelpProfileTypeLabel")}</Label>
          <HelpTip label={t("accessHelpProfileTypeLabel")} text="accessHelpProfileTypeTip" />
        </span>
        <Select value={kind} onValueChange={setKind}>
          <SelectTrigger className="w-40" aria-label={t("accessHelpProfileTypeLabel")}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="adult">{t("consoleProfilesKindAdult")}</SelectItem>
            <SelectItem value="managed">{t("accessHelpProfileTypeChild")}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <Button type="submit" disabled={busy || !name.trim()}>
        {t("consoleProfilesCreate")}
      </Button>
      {error && (
        <p role="alert" className="w-full text-sm text-[var(--state-danger)]">
          {t("consoleProfilesCreateFailed")}
        </p>
      )}
    </form>
  );
}

type AdminProfile = Omit<ProfileAdminInfo, "disabled">;

function ProfileRow({
  profile,
  index,
  count,
  locked,
  dragging,
  onDragging,
  onMove,
  onDropHere,
  onChanged,
}: {
  profile: AdminProfile;
  index: number;
  count: number;
  locked: boolean;
  dragging: string | null;
  onDragging: (id: string | null) => void;
  onMove: (from: number, to: number) => void;
  onDropHere: () => void;
  onChanged: () => void;
}) {
  const t = useT();
  const [name, setName] = useState(profile.name);
  const [kind, setKind] = useState(profile.kind);
  const [busy, setBusy] = useState(false);
  // Only a drag that starts on the grip moves the row, so text in the fields
  // still selects normally.
  const [armed, setArmed] = useState(false);
  const dirty = name.trim() !== profile.name || kind !== profile.kind;

  async function save() {
    const trimmed = name.trim();
    if (!trimmed || !dirty) return;
    setBusy(true);
    try {
      await updateProfile(profile.id, trimmed, kind);
      onChanged();
    } finally {
      setBusy(false);
    }
  }

  function onRowKeyDown(e: KeyboardEvent<HTMLLIElement>) {
    if (!e.altKey || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
    e.preventDefault();
    onMove(index, e.key === "ArrowUp" ? index - 1 : index + 1);
  }

  return (
    <li
      className={
        "console-card-pad bf-profile-row" + (dragging === profile.id ? " is-dragging" : "")
      }
      data-profile-id={profile.id}
      draggable={armed}
      onKeyDown={onRowKeyDown}
      onDragStart={(e) => {
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", profile.id);
        onDragging(profile.id);
      }}
      onDragOver={(e) => {
        if (dragging && dragging !== profile.id) e.preventDefault();
      }}
      onDrop={(e) => {
        e.preventDefault();
        onDropHere();
      }}
      onDragEnd={() => {
        setArmed(false);
        onDragging(null);
      }}
    >
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex items-center gap-1 self-center">
          <span
            className="bf-profile-grip"
            title={t("consoleProfilesDragHandle", { name: profile.name })}
            aria-hidden
            onPointerDown={() => setArmed(true)}
            onPointerUp={() => setArmed(false)}
          >
            <GripVertical className="size-4" />
          </span>
          <div className="flex flex-col">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-6"
              aria-label={t("consoleProfilesMoveUp", { name: profile.name })}
              disabled={locked || index === 0}
              onClick={() => onMove(index, index - 1)}
            >
              <ChevronUp aria-hidden />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-6"
              aria-label={t("consoleProfilesMoveDown", { name: profile.name })}
              disabled={locked || index === count - 1}
              onClick={() => onMove(index, index + 1)}
            >
              <ChevronDown aria-hidden />
            </Button>
          </div>
        </div>
        <CustomizeProfile profile={profile} onChanged={onChanged} />
        <div className="min-w-48 flex-1 space-y-1">
          <Label htmlFor={`profile-name-${profile.id}`}>{t("consoleProfilesNameLabel")}</Label>
          <Input
            id={`profile-name-${profile.id}`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoComplete="off"
          />
        </div>
        <div className="space-y-1">
          <Label>{t("accessHelpProfileTypeLabel")}</Label>
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger className="w-40" aria-label={t("accessHelpProfileTypeLabel")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="adult">{t("consoleProfilesKindAdult")}</SelectItem>
              <SelectItem value="managed">{t("accessHelpProfileTypeChild")}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <Button
          type="button"
          size="sm"
          disabled={busy || !dirty || !name.trim()}
          onClick={() => void save()}
        >
          {t("consoleProfilesSave")}
        </Button>
        <ConfirmButton
          label={t("consoleProfilesDisable")}
          confirm="consoleProfilesDisableConfirm"
          variant="outline"
          size="sm"
          onConfirm={async () => {
            await disableProfile(profile.id);
            onChanged();
          }}
        >
          {t("consoleProfilesDisableEffect")}
        </ConfirmButton>
      </div>
    </li>
  );
}

// The icon, colour and picture for one Profile, edited by an Admin Session.
function CustomizeProfile({
  profile,
  onChanged,
}: {
  profile: AdminProfile;
  onChanged: () => void;
}) {
  const t = useT();
  const [avatarFailed, setAvatarFailed] = useState(false);
  const title = t("consoleProfilesCustomizeTitle", { name: profile.name });

  return (
    <Dialog onOpenChange={() => setAvatarFailed(false)}>
      <DialogTrigger asChild>
        <button type="button" className="bf-profile-customize" aria-label={title}>
          <ProfileAvatar profile={profile} size="md" />
          <span className="text-xs text-muted-foreground">{t("consoleProfilesCustomize")}</span>
        </button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{t("consoleProfilesCustomizeLead")}</DialogDescription>
        </DialogHeader>
        <div className="space-y-6">
          <section className="space-y-3">
            <h3 className="m-0 text-sm font-semibold">{t("profilePhotoHeading")}</h3>
            <ProfilePhotoEditor profile={profile} onChanged={onChanged} />
          </section>
          <section className="space-y-3">
            <h3 className="m-0 text-sm font-semibold">{t("consoleProfilesAvatarHeading")}</h3>
            {avatarFailed && (
              <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
                {t("consoleProfilesAvatarFailed")}
              </p>
            )}
            <AvatarChooser
              profile={profile}
              onSave={async (token) => {
                setAvatarFailed(false);
                try {
                  await setProfileAvatar(profile.id, token);
                } catch (err) {
                  setAvatarFailed(true);
                  throw err;
                }
                onChanged();
              }}
              preview={false}
            />
          </section>
        </div>
        <DialogFooter>
          <DialogClose render={<Button type="button" />}>{t("consoleProfilesDone")}</DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
