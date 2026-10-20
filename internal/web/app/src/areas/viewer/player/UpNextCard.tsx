import { useT } from "@/lib/i18n";

// The Up Next card, ported from the player-up-next region (index.html) +
// renderUpNextCountdown (app.js): an "Up Next" eyebrow, the issued next Media's
// title, a live countdown, and Play now / Dismiss actions. role="region" with a
// label so AT users can find it; the countdown is aria-live so its decrement is
// announced. Rendered only while visible (the hook gates that).
export function UpNextCard({
  title,
  countdown,
  onPlay,
  onDismiss,
}: {
  title: string;
  countdown: number;
  onPlay: () => void;
  onDismiss: () => void;
}) {
  const t = useT();
  return (
    <div className="bf-up-next" role="region" aria-label={t("upNext")}>
      <span className="bf-up-next-title">{t("upNext")}</span>
      {title && <span className="bf-up-next-media">{title}</span>}
      <span className="bf-up-next-countdown" aria-live="polite">
        {t("upNextCountdownPrefix")}
        {Math.max(0, countdown)}
        {t("upNextCountdownSuffix")}
      </span>
      <div className="bf-up-next-actions">
        <button type="button" className="bf-control is-primary" onClick={onPlay}>
          {t("playNow")}
        </button>
        <button type="button" className="bf-control" onClick={onDismiss}>
          {t("dismiss")}
        </button>
      </div>
    </div>
  );
}
