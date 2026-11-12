import { useLocation } from "react-router";
import { HeaderSearch } from "./HeaderSearch";

// The viewer's header actions (right side of the AppShell): the search field
// (the search page has its own, so it is left out there). The current Profile's
// avatar, the way back to "Who's watching?" (ADR-0004, CORE-1196), My Data and
// Profile settings live in the AppShell's account menu (CORE-1131); the Reveal
// "Revealed" chip and Manager links are owned by the AppShell itself.
export function ViewerActions() {
  const { pathname } = useLocation();
  return pathname !== "/search" ? <HeaderSearch /> : null;
}
