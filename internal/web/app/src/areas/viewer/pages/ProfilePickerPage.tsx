import { Lock } from "lucide-react";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";
import { type Location, useLocation, useNavigate } from "react-router";
import { SignOutButton } from "@/areas/enroll";
import beamfallLogo from "@/assets/beamfall-logo.png";
import { markProfilePicked, ProfileAvatar } from "@/components/beamfall/profile";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { ProfileSummary } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import type { StringKey } from "@/strings";

// "Who's watching?" (CORE-1196; ADR-0004 profile switch). A full-page picker of
// large avatar tiles, shown on entry when the server has more than one Profile
// and one click away from the header afterwards.
//
// The server never says whether a Profile has an entry PIN: every switch to an
// adult Profile needs one, and a wrong PIN and "no PIN set" are the same
// refusal. So an adult tile opens the PIN prompt in place; a managed Profile
// switches straight away; the current Profile just continues.
//
// A switch clears the session spine before the request runs (see
// SessionProvider), which unmounts this page for the duration. A refused switch
// therefore remounts a fresh picker: the refusal is parked in `parkedFailure`
// so the new instance reopens the same Profile's PIN prompt with the reason,
// instead of dropping the visitor back on the grid with no explanation.

type FailureKind = "wrong" | "throttled" | "failed";

let parkedFailure: { profileID: string; kind: FailureKind } | null = null;

const FAILURE_MESSAGE: Record<FailureKind, StringKey> = {
  wrong: "whosWatchingPinWrong",
  throttled: "whosWatchingPinThrottled",
  failed: "couldNotSwitchProfile",
};

function failureKind(err: unknown, pinSent: boolean): FailureKind {
  const message = err instanceof Error ? err.message : "";
  if (message.includes("throttled")) return "throttled";
  if (pinSent && message.includes("PIN required")) return "wrong";
  return "failed";
}

/** Where the visitor was heading before the picker interrupted, else home. */
function destination(location: Location): string {
  const from = (location.state as { from?: Partial<Location> } | null)?.from;
  if (!from?.pathname || from.pathname === "/profiles") return "/";
  return from.pathname + (from.search ?? "");
}

export function ProfilePickerPage() {
  const t = useT();
  const navigate = useNavigate();
  const location = useLocation();
  const { profiles, session, switchProfile } = useSession();
  const currentProfileID = session?.profileID || "";
  const [failure, setFailure] = useState(parkedFailure);
  const [pinProfileID, setPinProfileID] = useState<string | null>(
    parkedFailure && parkedFailure.kind !== "failed" ? parkedFailure.profileID : null,
  );
  const [busyID, setBusyID] = useState<string | null>(null);

  // The parked refusal has been read into state; drop it when this instance
  // goes away so a later visit starts on the grid.
  useEffect(
    () => () => {
      parkedFailure = null;
    },
    [],
  );

  function done() {
    markProfilePicked();
    navigate(destination(location), { replace: true });
  }

  async function onSwitch(profileID: string, pin?: string) {
    setFailure(null);
    setBusyID(profileID);
    try {
      await switchProfile(profileID, pin);
      done();
    } catch (err) {
      const next = { profileID, kind: failureKind(err, pin !== undefined) };
      // Read by the picker instance that mounts once the session reloads.
      parkedFailure = next;
      setFailure(next);
      setBusyID(null);
    }
  }

  function onPick(profile: ProfileSummary) {
    if (profile.id === currentProfileID) return done();
    if (profile.kind === "managed") return void onSwitch(profile.id);
    setFailure(null);
    setPinProfileID(profile.id);
  }

  const pinProfile = profiles.find((p) => p.id === pinProfileID);

  return (
    <div className="flex min-h-dvh flex-col">
      <header className="flex items-center justify-between px-5 py-4 sm:px-8 sm:py-6">
        <div className="flex items-center gap-2.5">
          <img src={beamfallLogo} alt="" width={28} height={28} className="shrink-0" />
          <span className="text-base font-semibold tracking-tight">{t("brand")}</span>
        </div>
        <SignOutButton />
      </header>
      <main className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center px-4 py-8 sm:px-8">
        {pinProfile ? (
          <PinPrompt
            profile={pinProfile}
            busy={busyID === pinProfile.id}
            error={failure?.profileID === pinProfile.id ? t(FAILURE_MESSAGE[failure.kind]) : null}
            onSubmit={(pin) => void onSwitch(pinProfile.id, pin)}
            onBack={() => {
              setFailure(null);
              setPinProfileID(null);
            }}
          />
        ) : (
          <div className="flex flex-col items-center gap-8 pb-10 text-center">
            <div className="flex flex-col gap-2">
              <h1 className="text-3xl font-semibold tracking-tight sm:text-4xl">
                {t("whosWatchingTitle")}
              </h1>
              <p className="text-sm text-muted-foreground sm:text-base">{t("whosWatchingLead")}</p>
            </div>
            {failure && (
              <p role="alert" className="text-sm text-destructive">
                {t(FAILURE_MESSAGE[failure.kind])}
              </p>
            )}
            {profiles.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("profileUnavailable")}</p>
            ) : (
              <ul className="flex w-full flex-wrap justify-center gap-x-2 gap-y-4 sm:gap-x-6">
                {profiles.map((profile) => {
                  const current = profile.id === currentProfileID;
                  const needsPin = !current && profile.kind !== "managed";
                  return (
                    <li key={profile.id} className="w-[calc(50%-0.25rem)] list-none sm:w-44">
                      <button
                        type="button"
                        disabled={busyID !== null}
                        onClick={() => onPick(profile)}
                        aria-label={t(
                          current
                            ? "whosWatchingTileAriaCurrent"
                            : needsPin
                              ? "whosWatchingTileAriaPin"
                              : "whosWatchingTileAria",
                          { name: profile.name },
                        )}
                        className={"bf-who-tile" + (current ? " is-current" : "")}
                      >
                        <ProfileAvatar profile={profile} size="xl" />
                        <span className="max-w-full text-base font-semibold [overflow-wrap:anywhere]">
                          {profile.name}
                        </span>
                        <span className="flex min-h-[22px] flex-wrap items-center justify-center gap-1.5">
                          {current && <Badge variant="outline">{t("whosWatchingCurrent")}</Badge>}
                          {profile.kind === "managed" && (
                            <Badge variant="secondary">{t("managed")}</Badge>
                          )}
                          {needsPin && (
                            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                              <Lock aria-hidden className="size-3" />
                              {t("whosWatchingPinNeeded")}
                            </span>
                          )}
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        )}
      </main>
    </div>
  );
}

function PinPrompt({
  profile,
  busy,
  error,
  onSubmit,
  onBack,
}: {
  profile: ProfileSummary;
  busy: boolean;
  error: string | null;
  onSubmit: (pin: string) => void;
  onBack: () => void;
}) {
  const t = useT();
  const [pin, setPin] = useState("");
  const pinID = useId();
  const errorID = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => inputRef.current?.focus(), []);

  function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || pin === "") return;
    onSubmit(pin);
  }

  return (
    <div className="mx-auto w-full max-w-sm rounded-xl border bg-card p-6 text-card-foreground shadow-sm sm:p-8">
      <form onSubmit={submit} className="flex flex-col items-center gap-5 text-center" noValidate>
        <ProfileAvatar profile={profile} size="lg" />
        <div className="flex flex-col gap-1.5">
          <h1 className="text-2xl font-semibold tracking-tight">
            {t("whosWatchingPinTitle", { name: profile.name })}
          </h1>
          <p className="text-sm text-muted-foreground">{t("whosWatchingPinLead")}</p>
        </div>
        <div className="w-full space-y-1.5 text-left">
          <Label htmlFor={pinID}>{t("pin")}</Label>
          <Input
            ref={inputRef}
            id={pinID}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            value={pin}
            onChange={(e) => setPin(e.target.value)}
            aria-invalid={error != null}
            aria-describedby={error ? errorID : undefined}
            className="text-center text-lg tracking-[0.4em]"
          />
          {error && (
            <p id={errorID} role="alert" className="text-sm text-destructive">
              {error}
            </p>
          )}
        </div>
        <div className="flex w-full flex-col gap-2">
          <Button type="submit" size="lg" disabled={busy || pin === ""}>
            {t("whosWatchingContinue")}
          </Button>
          <Button type="button" variant="ghost" onClick={onBack} disabled={busy}>
            {t("whosWatchingBack")}
          </Button>
        </div>
      </form>
    </div>
  );
}
