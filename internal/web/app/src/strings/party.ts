import { partyGuestStrings as guest } from "./party-guest";
import { partyHostStrings as host } from "./party-host";
import { partyHostLiveStrings as live } from "./party-host-live";

// One catalog entry for the party session: host page, stage, live panels and guest phone page.
export const partyStrings = {
  en: { ...host.en, ...live.en, ...guest.en },
  fr: { ...host.fr, ...live.fr, ...guest.fr },
  de: { ...host.de, ...live.de, ...guest.de },
  es: { ...host.es, ...live.es, ...guest.es },
  pt: { ...host.pt, ...live.pt, ...guest.pt },
} as const;
