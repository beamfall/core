import { useState } from "react";
import { Link } from "react-router";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  type FeedSubscription,
  refreshSubscription,
  setSubscriptionDownloadPolicy,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { nodePath } from "@/lib/nodePath";
import {
  ALL_POLICIES,
  DEFAULT_LATEST_N,
  formatBytes,
  healthTone,
  isRateLimited,
  policyLabelKey,
} from "../../lib/feeds";
import { FeedHealth } from "./FeedHealth";

const SELECT_CLASS =
  "h-8 rounded-[var(--radius-control)] border border-input bg-transparent px-2 text-sm";

// The followed feeds of a Library (CORE-2957): each row names the feed, its
// entry and download counts, its download policy and its health, with a
// Refresh. A manager device can change the policy; Everything first shows the
// catalogue size and asks.
export function FeedList({
  nodeID,
  subscriptions,
  canChangePolicy,
  onChange,
}: {
  nodeID: string;
  subscriptions: readonly FeedSubscription[];
  canChangePolicy: boolean;
  onChange: (subscription: FeedSubscription) => void;
}) {
  return (
    <ul className="m-0 flex list-none flex-col gap-3 p-0">
      {subscriptions.map((subscription) => (
        <FeedRow
          key={subscription.id}
          nodeID={nodeID}
          subscription={subscription}
          canChangePolicy={canChangePolicy}
          onChange={onChange}
        />
      ))}
    </ul>
  );
}

type Notice = "refreshQueued" | "rateLimited" | "refreshFailed" | "policyFailed" | null;

function FeedRow({
  nodeID,
  subscription,
  canChangePolicy,
  onChange,
}: {
  nodeID: string;
  subscription: FeedSubscription;
  canChangePolicy: boolean;
  onChange: (subscription: FeedSubscription) => void;
}) {
  const t = useT();
  const locale = useDisplayLocale();
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState<Notice>(null);
  const [confirmEverything, setConfirmEverything] = useState(false);
  const title = subscription.title || t("feedsUntitled");
  const latestN = subscription.downloadLatestN || DEFAULT_LATEST_N;
  const tone = healthTone(subscription.health);

  async function refresh() {
    setRefreshing(true);
    setNotice(null);
    try {
      onChange(await refreshSubscription(subscription.id));
      setNotice("refreshQueued");
    } catch (err) {
      setNotice(isRateLimited(err) ? "rateLimited" : "refreshFailed");
    } finally {
      setRefreshing(false);
    }
  }

  async function applyPolicy(policy: string, confirmed = false) {
    setSaving(true);
    setNotice(null);
    try {
      onChange(
        await setSubscriptionDownloadPolicy(subscription.id, policy, {
          latestN: policy === "latest" ? latestN : undefined,
          confirmEverything: confirmed || undefined,
        }),
      );
    } catch {
      setNotice("policyFailed");
    } finally {
      setSaving(false);
    }
  }

  function choosePolicy(policy: string) {
    if (policy === subscription.downloadPolicy) return;
    if (policy === "everything") {
      setConfirmEverything(true);
      return;
    }
    void applyPolicy(policy);
  }

  const size = formatBytes(subscription.catalogBytes, locale);
  const noticeText =
    notice === "refreshQueued"
      ? t("feedsRefreshQueued")
      : notice === "rateLimited"
        ? t("feedsRefreshRateLimited")
        : notice === "refreshFailed"
          ? t("feedsRefreshFailed")
          : notice === "policyFailed"
            ? t("feedsPolicyFailed")
            : "";

  return (
    <li
      data-feed-row={subscription.id}
      data-health-tone={tone}
      className={`flex flex-col gap-2 rounded-[var(--radius-card)] border p-4 ${
        tone === "warning" ? "border-[var(--state-warning)]" : "border-[var(--border-subtle)]"
      }`}
    >
      <div className="flex flex-wrap items-start gap-x-4 gap-y-2">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <Link
            to={nodePath(nodeID, `/feeds/${encodeURIComponent(subscription.id)}`)}
            className="font-medium hover:underline"
            aria-label={t("feedsOpenCatalogue", { title })}
          >
            {title}
          </Link>
          <span className="text-sm text-[var(--text-muted)]">
            {t("feedsCounts", {
              entries: subscription.entryCount,
              downloaded: subscription.downloadedCount,
            })}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {canChangePolicy ? (
            <select
              aria-label={t("feedsPolicyFor", { title })}
              className={SELECT_CLASS}
              value={subscription.downloadPolicy}
              disabled={saving}
              onChange={(event) => choosePolicy(event.target.value)}
            >
              {ALL_POLICIES.map((policy) => (
                <option key={policy} value={policy}>
                  {t(policyLabelKey(policy), { count: latestN })}
                </option>
              ))}
            </select>
          ) : (
            <span className="text-sm text-[var(--text-secondary)]">
              {t(policyLabelKey(subscription.downloadPolicy), { count: latestN })}
            </span>
          )}
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={refreshing}
            aria-label={t("feedsRefreshLabel", { title })}
            onClick={() => void refresh()}
          >
            {refreshing ? t("feedsRefreshing") : t("feedsRefresh")}
          </Button>
        </div>
      </div>
      <FeedHealth health={subscription.health} />
      {noticeText && (
        <p
          role={notice === "refreshQueued" ? "status" : "alert"}
          className={`m-0 text-sm ${
            notice === "refreshFailed" || notice === "policyFailed"
              ? "text-[var(--state-danger)]"
              : "text-[var(--text-muted)]"
          }`}
        >
          {noticeText}
        </p>
      )}
      <AlertDialog open={confirmEverything} onOpenChange={setConfirmEverything}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("feedsEverythingTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {size
                ? t("feedsEverythingBody", { title, size })
                : t("feedsEverythingUnknownSize", { title })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction onClick={() => void applyPolicy("everything", true)}>
              {t("feedsEverythingConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </li>
  );
}
