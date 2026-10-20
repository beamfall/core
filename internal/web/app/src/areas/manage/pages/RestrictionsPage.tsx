import { useState } from "react";
import { ErrorLine, ManageSection, StatusBadge } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

// Restrictions (ADR-0004 Reveal contract, ported from index.html
// restrictions-view): the PIN reveals restricted content for this session.
// Reveal/Relock state is visible STATE, never a hidden mode — the Revealed chip
// renders it. The PIN goes into the mutation and nowhere else; the field clears
// as soon as it is read. Error copy stays calm and fixed and never exposes
// security internals.
export function RestrictionsPage() {
  const t = useT();
  const { revealed, reveal, relock } = useSession();
  const [pin, setPin] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const onReveal = async (event: React.FormEvent) => {
    event.preventDefault();
    setError("");
    const value = pin;
    setPin("");
    if (!value) return;
    setBusy(true);
    try {
      const ok = await reveal(value);
      if (!ok) setError(t("pinIncorrect"));
    } catch (err) {
      setError(
        err instanceof Error && err.message === "reveal attempts throttled"
          ? t("tooManyAttempts")
          : t("pinIncorrect"),
      );
    } finally {
      setBusy(false);
    }
  };

  const onRelock = async () => {
    setBusy(true);
    setError("");
    try {
      await relock();
    } finally {
      setBusy(false);
    }
  };

  return (
    <ManageSection
      title={t("restrictionsTitle")}
      lead={t("restrictionsLead")}
      actions={revealed ? <StatusBadge state="revealed">{t("revealed")}</StatusBadge> : undefined}
    >
      {revealed ? (
        <Button type="button" variant="outline" onClick={onRelock} disabled={busy}>
          {t("relock")}
        </Button>
      ) : (
        <form onSubmit={onReveal} className="flex items-end gap-3">
          <div className="space-y-1">
            <Label htmlFor="reveal-pin">{t("pin")}</Label>
            <Input
              id="reveal-pin"
              type="password"
              inputMode="numeric"
              autoComplete="off"
              value={pin}
              onChange={(e) => setPin(e.target.value)}
              className="w-40"
            />
          </div>
          <Button type="submit" disabled={busy}>
            {t("reveal")}
          </Button>
        </form>
      )}
      <ErrorLine message={error} />
    </ManageSection>
  );
}
