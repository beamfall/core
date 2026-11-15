import { useT } from "@/lib/i18n";

// The "Are you still watching?" prompt (CORE-1689): shown in place of the next
// autoplay once the run of autoplayed items reaches the Profile's setting.
// Playback stays stopped until the viewer chooses. It reuses the Up Next card's
// placement and controls, and focuses Continue so a keyboard or remote can
// answer at once.
export function StillWatchingPrompt({
  title,
  onContinue,
  onStop,
}: {
  title: string;
  onContinue: () => void;
  onStop: () => void;
}) {
  const t = useT();
  return (
    <div
      className="bf-up-next"
      role="alertdialog"
      aria-labelledby="bf-still-watching-title"
      aria-describedby={title ? "bf-still-watching-next" : undefined}
    >
      <span id="bf-still-watching-title" className="bf-up-next-title">
        {t("stillWatchingTitle")}
      </span>
      {title && (
        <span id="bf-still-watching-next" className="bf-up-next-media">
          {title}
        </span>
      )}
      <div className="bf-up-next-actions">
        <button type="button" className="bf-control is-primary" onClick={onContinue} autoFocus>
          {t("stillWatchingContinue")}
        </button>
        <button type="button" className="bf-control" onClick={onStop}>
          {t("stillWatchingStop")}
        </button>
      </div>
    </div>
  );
}
