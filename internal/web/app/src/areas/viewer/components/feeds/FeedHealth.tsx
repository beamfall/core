import { formatLocalTime, formatRelativeTime } from "@/lib/activityDisplay";
import type { SubscriptionHealth } from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { errorClassKey, healthTone, parseWireTime } from "../../lib/feeds";

// A followed feed's health line (CORE-2955): ok and pending stay quiet,
// failing names the cause in plain words with when it started and the next
// try, a week of failing turns amber, and a retired feed says so.
export function FeedHealth({
  health,
  lastPolledAt,
  now = new Date(),
}: {
  health: SubscriptionHealth | null | undefined;
  lastPolledAt?: string | null;
  now?: Date;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const tone = healthTone(health);

  if (tone === "retired") {
    return (
      <p data-health="retired" className="m-0 text-sm text-[var(--text-secondary)]">
        {t("feedsHealthRetired")}
      </p>
    );
  }
  if (tone === "quiet") {
    const success = parseWireTime(lastPolledAt);
    const pending = health?.healthStatus === "ok" && !lastPolledAt;
    const text = success
      ? t("feedsHealthOK", { when: formatRelativeTime(success, now, locale) })
      : t(pending ? "feedsHealthPending" : "feedsHealthOKNever");
    return (
      <p
        data-health={pending ? "pending" : health?.healthStatus || "ok"}
        className="m-0 text-sm text-[var(--text-muted)]"
      >
        {text}
      </p>
    );
  }

  const cause = t(errorClassKey(health?.lastErrorClass));
  const since = parseWireTime(health?.failingSince);
  const next = parseWireTime(health?.nextPollAt);
  const colour = tone === "warning" ? "text-[var(--state-warning)]" : "text-[var(--state-danger)]";
  return (
    <div data-health={tone} role="status" className="flex flex-col gap-0.5 text-sm">
      <p className={`m-0 font-medium ${colour}`}>
        {t(tone === "warning" ? "feedsHealthWarning" : "feedsHealthFailing", { cause })}
      </p>
      {(since || next) && (
        <p className="m-0 text-[var(--text-muted)]">
          {[
            since && t("feedsFailingSince", { date: formatLocalTime(since, locale) }),
            next && t("feedsNextTry", { when: formatRelativeTime(next, now, locale) }),
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
      )}
    </div>
  );
}
