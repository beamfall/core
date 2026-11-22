// Feed follow, feed home and catalogue operations (CORE-2957, ADR-0238):
// search the plugin's feed directory, follow a public feed with a download
// policy, list followed feeds with their health, refresh one, change its
// policy, and read its catalogue. The feed URL is secret-class: these
// selections never read it back (a search result carries the public address
// the person is about to follow, nothing more).

import { graphql } from "@/lib/api";
import type {
  FeedSearchResult,
  FeedSubscription,
  FollowInfo,
  SubscriptionEntry,
  SubscriptionRefreshInfo,
} from "./types";

const FEED_SUBSCRIPTION_FIELDS =
  "id libraryID title downloadPolicy downloadLatestN healthStatus lastErrorClass failingSince nextPollAt lastPolledAt entryCount downloadedCount catalogBytes followed newEntryRoute";

const SEARCH_FEEDS_QUERY =
  "query SearchFeeds($query: String!) { searchFeeds(query: $query) { title feedURL artworkURL description } }";
const FEED_SUBSCRIPTIONS_QUERY = `query FeedSubscriptions($libraryID: String!) { subscriptions(libraryID: $libraryID) { ${FEED_SUBSCRIPTION_FIELDS} } }`;
const FEED_SUBSCRIPTION_IDS_QUERY =
  "query FeedSubscriptionIDs($libraryID: String!) { subscriptions(libraryID: $libraryID) { id } }";
const FOLLOW_FEED_MUTATION = `mutation FollowFeed($feedURL: String!, $libraryID: String, $downloadPolicy: String, $latestN: Int) { followFeed(feedURL: $feedURL, libraryID: $libraryID, downloadPolicy: $downloadPolicy, latestN: $latestN) { ${FEED_SUBSCRIPTION_FIELDS} } }`;
const REFRESH_SUBSCRIPTION_MUTATION = `mutation RefreshSubscription($id: String!) { refreshSubscription(id: $id) { id title healthStatus lastErrorClass failingSince nextPollAt } }`;
const SET_DOWNLOAD_POLICY_MUTATION = `mutation SetSubscriptionDownloadPolicy($id: String!, $downloadPolicy: String!, $latestN: Int, $confirmEverything: Boolean) { setSubscriptionDownloadPolicy(id: $id, downloadPolicy: $downloadPolicy, latestN: $latestN, confirmEverything: $confirmEverything) { ${FEED_SUBSCRIPTION_FIELDS} } }`;
const SET_FOLLOW_NEW_ENTRY_ROUTE_MUTATION =
  "mutation SetFollowNewEntryRoute($subscriptionID: String!, $route: String!) { setFollowNewEntryRoute(subscriptionID: $subscriptionID, route: $route) { id title followed newEntryRoute } }";
const QUEUE_NEXT_MUTATION =
  'mutation QueueNext($mediaIDs: [String!]!) { queueAddMany(mediaIDs: $mediaIDs, placement: "next") { currentEntryID } }';
const SUBSCRIPTION_ENTRIES_QUERY =
  "query SubscriptionEntries($subscriptionID: String!) { subscriptionEntries(subscriptionID: $subscriptionID) { mediaID title publishedAt feedPosition enclosureLength downloadState availability played } }";

/** Search the activated plugins' feed directories. Adult Profile or manager device. */
export async function searchFeeds(query: string): Promise<FeedSearchResult[]> {
  const data = await graphql<{ searchFeeds: FeedSearchResult[] | null }>(SEARCH_FEEDS_QUERY, {
    query,
  });
  return data.searchFeeds ?? [];
}

/** The followed feeds of one Library, with health and catalogue counts. */
export async function fetchFeedSubscriptions(libraryID: string): Promise<FeedSubscription[]> {
  const data = await graphql<{ subscriptions: FeedSubscription[] | null }>(
    FEED_SUBSCRIPTIONS_QUERY,
    { libraryID },
  );
  return data.subscriptions ?? [];
}

/** Whether a Library has any followed feed: the cheap probe behind the
 * library's Following pivot. */
export async function libraryHasFeedSubscriptions(libraryID: string): Promise<boolean> {
  const data = await graphql<{ subscriptions: { id: string }[] | null }>(
    FEED_SUBSCRIPTION_IDS_QUERY,
    { libraryID },
  );
  return (data.subscriptions?.length ?? 0) > 0;
}

/** Follow a public feed. The server defaults the policy to latest. */
export async function followFeed(
  feedURL: string,
  options: { libraryID?: string; downloadPolicy?: string; latestN?: number } = {},
): Promise<FeedSubscription> {
  const data = await graphql<{ followFeed: FeedSubscription }>(FOLLOW_FEED_MUTATION, {
    feedURL,
    libraryID: options.libraryID ?? null,
    downloadPolicy: options.downloadPolicy ?? null,
    latestN: options.latestN ?? null,
  });
  return data.followFeed;
}

/** Ask for a poll now. The server refuses with THROTTLED inside five minutes
 * of the last one. */
export async function refreshSubscription(id: string): Promise<SubscriptionRefreshInfo> {
  const data = await graphql<{ refreshSubscription: SubscriptionRefreshInfo }>(
    REFRESH_SUBSCRIPTION_MUTATION,
    { id },
  );
  return data.refreshSubscription;
}

/** Change a Subscription's download policy. `everything` needs
 * confirmEverything, sent only after the person saw the catalogue size. */
export async function setSubscriptionDownloadPolicy(
  id: string,
  downloadPolicy: string,
  options: { latestN?: number; confirmEverything?: boolean } = {},
): Promise<FeedSubscription> {
  const data = await graphql<{ setSubscriptionDownloadPolicy: FeedSubscription }>(
    SET_DOWNLOAD_POLICY_MUTATION,
    {
      id,
      downloadPolicy,
      latestN: options.latestN ?? null,
      confirmEverything: options.confirmEverything ?? null,
    },
  );
  return data.setSubscriptionDownloadPolicy;
}

/** A Subscription's catalogue, every entry whether downloaded or not. */
export async function fetchSubscriptionEntries(
  subscriptionID: string,
): Promise<SubscriptionEntry[]> {
  const data = await graphql<{ subscriptionEntries: SubscriptionEntry[] | null }>(
    SUBSCRIPTION_ENTRIES_QUERY,
    { subscriptionID },
  );
  return data.subscriptionEntries ?? [];
}

/** Where new entries of a followed feed go for the calling Profile: new
 * (listed only), queue-end or queue-front (CORE-2958). */
export async function setFollowNewEntryRoute(
  subscriptionID: string,
  route: string,
): Promise<FollowInfo> {
  const data = await graphql<{ setFollowNewEntryRoute: FollowInfo }>(
    SET_FOLLOW_NEW_ENTRY_ROUTE_MUTATION,
    { subscriptionID, route },
  );
  return data.setFollowNewEntryRoute;
}

/** Put one Media right after the current item of the Profile's play queue. */
export async function queuePlayNextMedia(mediaID: string): Promise<void> {
  await graphql<{ queueAddMany: unknown }>(QUEUE_NEXT_MUTATION, { mediaIDs: [mediaID] });
}
