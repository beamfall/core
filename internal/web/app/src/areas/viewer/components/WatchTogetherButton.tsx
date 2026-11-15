import { Users } from "lucide-react";
import { useState } from "react";
import { useNavigate } from "react-router";
import { buttonVariants } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import type { PlayerNavState } from "../lib/watchQueue";
import { useHomeNetwork, watchPartyInviteState } from "../lib/watchTogether";
import { getWatchParty, startWatchParty, useWatchPartyState } from "../player/watchPartyStore";

// "Watch together" on the media detail page (CORE-1764): a ghost secondary
// shown only on the home network and while no party is running. It starts a
// party around the item held at the viewer's position, then opens the player
// with the invite panel open. Failure stays on the page as a short notice.
export function WatchTogetherButton({
  mediaID,
  title,
  positionSeconds,
}: {
  mediaID: string;
  title: string;
  positionSeconds?: number;
}) {
  const t = useT();
  const navigate = useNavigate();
  const home = useHomeNetwork();
  const party = useWatchPartyState();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState<"" | "home-network" | "failed">("");

  if (!home || (party.status !== "idle" && !busy)) return null;

  async function start() {
    setBusy(true);
    setFailed("");
    const positionMS = Math.max(0, Math.round((positionSeconds ?? 0) * 1000));
    const ok = await startWatchParty(mediaID, positionMS);
    setBusy(false);
    if (!ok) {
      setFailed(getWatchParty().error === "home-network" ? "home-network" : "failed");
      return;
    }
    // The party timeline already holds the start position; skip pre-play so the
    // player opens straight on the title the party is watching.
    const state: PlayerNavState & { skipPrePlay: true } = {
      title,
      skipPrePlay: true,
      ...watchPartyInviteState(),
    };
    navigate(`/watch/${encodeURIComponent(mediaID)}`, { state });
  }

  return (
    <>
      <button
        type="button"
        className={`${buttonVariants({ variant: "ghost", size: "lg" })} bf-detail-secondary`}
        aria-label={t("mediaDetailWatchTogetherLabel", { title })}
        aria-busy={busy || undefined}
        disabled={busy}
        onClick={() => void start()}
      >
        <Users aria-hidden />
        {t("mediaDetailWatchTogether")}
      </button>
      {failed && (
        <p className="bf-detail-notice" role="alert">
          {t(
            failed === "home-network"
              ? "mediaDetailWatchTogetherHomeOnly"
              : "mediaDetailWatchTogetherFailed",
          )}
        </p>
      )}
    </>
  );
}
