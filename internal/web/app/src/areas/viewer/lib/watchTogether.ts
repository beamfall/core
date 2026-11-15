import { useEffect, useState } from "react";
import { calibrateWatchPartyClock } from "@/lib/graphql";

// The media detail page's way into a Watch Party (CORE-1764). It reuses the
// player's create path (watchPartyStore.startWatchParty, CORE-1677) and hands
// the player one navigation-state flag that opens the invite panel; nothing
// here changes the party store or its panel.

/** The nav-state key the player reads to open its Watch Party panel on arrival. */
const INTENT_KEY = "watchPartyIntent";
const INVITE = "invite";

/** Nav-state fields that ask the player to open its Watch Party invite panel. */
export function watchPartyInviteState(): { [INTENT_KEY]: typeof INVITE } {
  return { [INTENT_KEY]: INVITE };
}

/** Whether nav state asks the player to open its Watch Party invite panel. */
export function readWatchPartyIntent(state: unknown): boolean {
  return (
    !!state &&
    typeof state === "object" &&
    (state as Record<string, unknown>)[INTENT_KEY] === INVITE
  );
}

// Watch Party works on the home network only (CORE-0531): the server refuses
// the clock calibration from anywhere else. One probe per tab answers whether
// to offer the action; a failed probe means "not here".
let probe: Promise<boolean> | null = null;

function probeHomeNetwork(): Promise<boolean> {
  probe ??= calibrateWatchPartyClock().then(
    () => true,
    () => false,
  );
  return probe;
}

/** Forgets the cached probe (tests). */
export function resetHomeNetworkProbeForTest(): void {
  probe = null;
}

/** True once the server confirmed this tab is on its home network; false until then. */
export function useHomeNetwork(): boolean {
  const [home, setHome] = useState(false);
  useEffect(() => {
    let alive = true;
    void probeHomeNetwork().then((answer) => {
      if (alive) setHome(answer);
    });
    return () => {
      alive = false;
    };
  }, []);
  return home;
}
