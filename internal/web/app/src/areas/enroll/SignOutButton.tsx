import { type ComponentProps, useState } from "react";
import { Button } from "@/components/ui/button";
import { logout } from "@/lib/graphql";
import { useT } from "@/lib/i18n";

// Sign-out (ADR-0007): drop the session cookie, then reload to BASE_URL so the
// session spine re-bootstraps from a clean slate. Faithful to the vanilla
// sign-out (internal/web/static/app.js): the logout POST is best-effort — if the
// cookie is already gone the request may fail, and we still reload, which lands
// the browser back on the enroll gate (SessionProvider surfaces needsEnroll).
//
// Reusable: the shared chrome and any area can drop this in as a header action.
export function SignOutButton({
  variant = "ghost",
  size = "sm",
  ...props
}: Omit<ComponentProps<typeof Button>, "onClick" | "children">) {
  const t = useT();
  const [busy, setBusy] = useState(false);

  async function onClick() {
    if (busy) return;
    setBusy(true);
    try {
      await logout();
    } catch {
      // Cookies may already be gone; reload regardless to reach the enroll gate.
    }
    window.location.assign(import.meta.env.BASE_URL);
  }

  return (
    <Button
      type="button"
      variant={variant}
      size={size}
      disabled={busy}
      onClick={onClick}
      {...props}
    >
      {t("signOut")}
    </Button>
  );
}
