import { ChevronLeft } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useId, useState } from "react";
import { Link, Navigate, useLocation, useParams } from "react-router";
import { AppShell } from "@/components/chrome/AppShell";
import { Button } from "@/components/ui/button";
import { displayString } from "@/lib/display";
import {
  type FeedSubscription,
  type FollowInfo,
  fetchFeedSubscriptions,
  fetchSubscriptionEntries,
  fetchSubscriptionFollow,
  type NavigationNode,
  type SubscriptionEntry,
  setFollowNewEntryRoute,
} from "@/lib/graphql";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { findNodeBySlug, nodePath, nodeSlug } from "@/lib/nodePath";
import { useSession } from "@/lib/session";
import { FeedCatalogue } from "../components/feeds/FeedCatalogue";
import { FeedFollowPanel } from "../components/feeds/FeedFollowPanel";
import { FeedHealth } from "../components/feeds/FeedHealth";
import { FeedList } from "../components/feeds/FeedList";
import { FeedRouteSelect } from "../components/feeds/FeedRouteSelect";
import { LibraryHeader } from "../components/LibraryPivots";
import { ViewerActions } from "../components/ViewerActions";
import { isForbidden, markLibraryHasFeeds, upsertSubscription } from "../lib/feeds";

// A Library's Following pivot (CORE-2957) at /n/:nodeId/feeds, and one
// followed feed's catalogue at /n/:nodeId/feeds/:subscriptionId. Both sit
// under the Library whose plugin takes the feed's entries, headed by the
// Library's own (plugin-provided) name, so the screens carry no domain words.

/** Resolve the Library node the way the other pivots do; null renders `fallback`. */
function useFeedLibraryNode(subpath: string): { node: NavigationNode | null; fallback: ReactNode } {
  const t = useT();
  const { nodeId } = useParams();
  const location = useLocation();
  const { manifest, loading } = useSession();
  if (loading && !manifest) {
    return {
      node: null,
      fallback: (
        <AppShell actions={<ViewerActions />}>
          <p className="text-sm text-[var(--text-muted)]">{t("loading")}</p>
        </AppShell>
      ),
    };
  }
  const node = findNodeBySlug(
    (manifest?.navigation || []).filter((entry) => entry.view === "layout"),
    nodeId,
  );
  if (!node?.libraryID) {
    return {
      node: null,
      fallback: <Navigate to={node ? nodePath(node.id, "", node.libraryID) : "/"} replace />,
    };
  }
  if (nodeId !== nodeSlug(node.id, node.libraryID)) {
    return {
      node: null,
      fallback: (
        <Navigate
          to={{ pathname: nodePath(node.id, subpath, node.libraryID), search: location.search }}
          replace
        />
      ),
    };
  }
  return { node, fallback: null };
}

type ListState =
  | { kind: "loading" }
  | { kind: "failed" }
  | { kind: "managerOnly" }
  | { kind: "ready"; items: FeedSubscription[] };

export function LibraryFeedsPage() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const { session } = useSession();
  const { node, fallback } = useFeedLibraryNode("/feeds");
  const libraryID = node?.libraryID ?? "";
  const listID = useId();
  const [list, setList] = useState<ListState>({ kind: "loading" });
  const [followedHere, setFollowedHere] = useState<FeedSubscription[]>([]);
  const [attempt, setAttempt] = useState(0);
  const manager = session?.deviceRole === "manager";
  const canFollow = session?.profile === "adult" || manager;

  useEffect(() => {
    if (!libraryID) return;
    let live = true;
    setList({ kind: "loading" });
    fetchFeedSubscriptions(libraryID)
      .then((items) => {
        if (live) setList({ kind: "ready", items });
      })
      .catch((err) => {
        if (live) setList({ kind: isForbidden(err) ? "managerOnly" : "failed" });
      });
    return () => {
      live = false;
    };
  }, [libraryID, attempt]);

  const update = useCallback((subscription: FeedSubscription) => {
    setList((prev) =>
      prev.kind === "ready"
        ? { kind: "ready", items: upsertSubscription(prev.items, subscription) }
        : prev,
    );
    setFollowedHere((prev) => upsertSubscription(prev, subscription));
  }, []);

  const onFollowed = useCallback(
    (subscription: FeedSubscription) => {
      markLibraryHasFeeds(subscription.libraryID || libraryID);
      update(subscription);
    },
    [libraryID, update],
  );

  if (!node) return fallback;

  // A device that cannot read the list still sees the feeds it followed here.
  const items = list.kind === "ready" ? list.items : followedHere;
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-6">
        <LibraryHeader
          nodeID={node.id}
          libraryID={libraryID}
          heading={displayString(node.title, displayLocale)}
        />
        {canFollow && <FeedFollowPanel libraryID={libraryID} onFollowed={onFollowed} />}
        <section aria-labelledby={listID} className="flex flex-col gap-3">
          <h2 id={listID} className="bf-section-title m-0 text-lg font-semibold">
            {t("feedsListHeading")}
          </h2>
          {list.kind === "loading" && items.length === 0 && (
            <p className="m-0 text-sm text-[var(--text-muted)]">{t("loading")}</p>
          )}
          {list.kind === "failed" && (
            <div role="alert" className="flex flex-col gap-2">
              <p className="m-0 text-sm text-[var(--text-secondary)]">{t("feedsLoadFailed")}</p>
              <Button
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => setAttempt((n) => n + 1)}
              >
                {t("jobRetryAction")}
              </Button>
            </div>
          )}
          {list.kind === "managerOnly" && (
            <p className="m-0 text-sm text-[var(--text-muted)]">{t("feedsListManagerOnly")}</p>
          )}
          {list.kind === "ready" && items.length === 0 && (
            <p className="bf-empty m-0 text-sm text-[var(--text-muted)]">
              {canFollow ? t("feedsEmpty") : t("feedsEmptyViewer")}
            </p>
          )}
          {items.length > 0 && (
            <FeedList
              libraryPath={nodePath(node.id, "", node.libraryID)}
              subscriptions={items}
              canChangePolicy={manager}
              onChange={update}
            />
          )}
        </section>
      </div>
    </AppShell>
  );
}

type CatalogueState =
  | { kind: "loading" }
  | { kind: "failed" }
  | {
      kind: "ready";
      entries: SubscriptionEntry[];
      subscription: FeedSubscription | null;
      follow: FollowInfo | null;
    };

export function FeedCataloguePage() {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const { subscriptionId = "" } = useParams();
  const { node, fallback } = useFeedLibraryNode(`/feeds/${encodeURIComponent(subscriptionId)}`);
  const libraryID = node?.libraryID ?? "";
  const [state, setState] = useState<CatalogueState>({ kind: "loading" });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!libraryID || !subscriptionId) return;
    let live = true;
    setState({ kind: "loading" });
    // The feed's name and health come from the Library's list when this
    // device may read it; the catalogue reads on its own either way.
    const subscription = fetchFeedSubscriptions(libraryID)
      .then((items) => items.find((item) => item.id === subscriptionId) ?? null)
      .catch(() => null);
    // The follow state is the Profile's own and readable on any device.
    const follow = fetchSubscriptionFollow(subscriptionId).catch(() => null);
    Promise.all([fetchSubscriptionEntries(subscriptionId), subscription, follow])
      .then(([entries, found, followed]) => {
        if (live) setState({ kind: "ready", entries, subscription: found, follow: followed });
      })
      .catch(() => {
        if (live) setState({ kind: "failed" });
      });
    return () => {
      live = false;
    };
  }, [libraryID, subscriptionId, attempt]);

  if (!node) return fallback;

  const subscription = state.kind === "ready" ? state.subscription : null;
  const follow = state.kind === "ready" ? state.follow : null;
  const heading = subscription?.title || displayString(node.title, displayLocale);
  return (
    <AppShell actions={<ViewerActions />}>
      <div className="flex flex-col gap-5">
        <Link
          to={nodePath(node.id, "/feeds", node.libraryID)}
          className="flex items-center gap-1 self-start text-sm text-[var(--text-secondary)] hover:underline"
        >
          <ChevronLeft aria-hidden className="size-4" />
          {t("feedsCatalogueBack")}
        </Link>
        {/* The pivots stay off so opening one feed does not become the
            Library's remembered pivot. */}
        <LibraryHeader nodeID={node.id} libraryID={libraryID} heading={heading} pivots={false} />
        {subscription && (
          <FeedHealth health={subscription} lastPolledAt={subscription.lastPolledAt} />
        )}
        {follow?.followed && (
          <FeedRouteSelect
            route={follow.newEntryRoute || "new"}
            onChange={async (route) => {
              const saved = await setFollowNewEntryRoute(follow.id, route);
              // A save that settles after leaving for another feed must not
              // overwrite that feed's route.
              setState((prev) =>
                prev.kind === "ready" && prev.follow?.id === saved.id
                  ? { ...prev, follow: saved }
                  : prev,
              );
            }}
          />
        )}
        {state.kind === "loading" && (
          <p className="m-0 text-sm text-[var(--text-muted)]">{t("loading")}</p>
        )}
        {state.kind === "failed" && (
          <div role="alert" className="flex flex-col gap-2">
            <p className="m-0 text-sm text-[var(--text-secondary)]">
              {t("feedsCatalogueLoadFailed")}
            </p>
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              onClick={() => setAttempt((n) => n + 1)}
            >
              {t("jobRetryAction")}
            </Button>
          </div>
        )}
        {state.kind === "ready" && state.entries.length === 0 && (
          <p className="bf-empty m-0 text-sm text-[var(--text-muted)]">
            {t("feedsCatalogueEmpty")}
          </p>
        )}
        {state.kind === "ready" && state.entries.length > 0 && (
          <FeedCatalogue entries={state.entries} />
        )}
      </div>
    </AppShell>
  );
}
