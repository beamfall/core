package graph

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Feed follow, refresh, download policy and catalogue reads (CORE-2953,
// CORE-2955, CORE-2957; ADR-0238). The feed URL is a secret-adjacent value:
// it is never echoed in an error, and only manager sessions see it in
// SubscriptionInfo. Enclosure URLs never leave the server.

// FeedProbe is what a probe of a feed reports before it is followed.
type FeedProbe struct {
	Title        string
	EntryCount   int
	CatalogBytes int64
}

// FeedProber fetches and parses a public feed without storing anything.
type FeedProber interface {
	ProbeFeed(ctx context.Context, feedURL string) (FeedProbe, error)
}

// SubscriptionPoller polls one Subscription as soon as it can. It must not
// block the request: it schedules the poll and returns.
type SubscriptionPoller interface {
	PollSubscriptionNow(ctx context.Context, id store.SubscriptionID)
}

// subscriptionCatalogue is the store's feed-catalogue surface the graph uses.
type subscriptionCatalogue interface {
	SubscriptionDownloadPolicy(ctx context.Context, id store.SubscriptionID) (store.SubscriptionPolicy, error)
	SubscriptionEntries(ctx context.Context, id store.SubscriptionID) ([]store.SubscriptionEntry, error)
	SetSubscriptionDownloadPolicy(ctx context.Context, id store.SubscriptionID, policy store.SubscriptionPolicy) error
}

// SetFeedProber wires the probe followFeed runs before creating a
// Subscription. Without one, followFeed reports that probing is unavailable.
func (h *Handler) SetFeedProber(prober FeedProber) {
	h.feedProber = prober
}

// SetFeedErrorClassifier wires the mapping from a probe error to its failure
// class. Only the class reaches the client.
func (h *Handler) SetFeedErrorClassifier(classify func(error) store.FeedErrorClass) {
	h.feedErrorClass = classify
}

// SetSubscriptionPoller wires the poller kicked after a follow or a download
// policy change.
func (h *Handler) SetSubscriptionPoller(poller SubscriptionPoller) {
	h.feedPoller = poller
}

func (h *Handler) catalogue() subscriptionCatalogue {
	if h.feedCatalogue != nil {
		return h.feedCatalogue
	}
	return h.store
}

func (h *Handler) pollSubscription(ctx context.Context, id store.SubscriptionID) {
	if h.feedPoller != nil {
		h.feedPoller.PollSubscriptionNow(ctx, id)
	}
}

func (h *Handler) withSubscriptionCatalogue(ctx context.Context, info SubscriptionInfo, subscription store.Subscription) (SubscriptionInfo, error) {
	policy, err := h.catalogue().SubscriptionDownloadPolicy(ctx, subscription.ID)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	entries, err := h.catalogue().SubscriptionEntries(ctx, subscription.ID)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	info.DownloadPolicy = string(policy.Policy)
	info.DownloadLatestN = policy.LatestN
	info.EntryCount = len(entries)
	for _, entry := range entries {
		if entryDownloaded(entry) {
			info.DownloadedCount++
		}
		if entry.EnclosureLength > 0 {
			info.CatalogBytes += entry.EnclosureLength
		}
	}
	return info, nil
}

func entryDownloaded(entry store.SubscriptionEntry) bool {
	return entry.Downloaded || entry.DownloadState == store.EntryDownloadDownloaded
}

// viewerSafeSubscription blanks the manager-only fields (feedURL, root) for a
// caller without the manager role.
func viewerSafeSubscription(scope auth.Scope, info SubscriptionInfo) SubscriptionInfo {
	if scope.DeviceRole != store.DeviceRoleManager {
		info.FeedURL = ""
		info.Root = ""
	}
	return info
}

func canFollowFeeds(scope auth.Scope) bool {
	return scope.Profile == store.ProfileAdult
}

// publicFeedURL accepts an absolute http(s) URL without credentials.
func publicFeedURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// downloadPolicyVariables reads downloadPolicy and latestN. A missing policy
// is the default (latest, N=3).
func downloadPolicyVariables(req request, required bool) (store.SubscriptionPolicy, error) {
	policy := store.DownloadPolicy(trimmedVariable(req, "downloadPolicy"))
	if policy == "" {
		if required {
			return store.SubscriptionPolicy{}, clientError("downloadPolicy is required")
		}
		policy = store.DownloadPolicyLatest
	}
	switch policy {
	case store.DownloadPolicyLatest, store.DownloadPolicyNewOnly, store.DownloadPolicyEverything, store.DownloadPolicyStreamOnly:
	default:
		return store.SubscriptionPolicy{}, clientError("downloadPolicy is not a supported policy")
	}
	result := store.SubscriptionPolicy{Policy: policy}
	v := newVars(req)
	if number, ok := v.Number("latestN"); ok {
		result.LatestN = int(number)
	}
	if err := v.Err(); err != nil {
		return store.SubscriptionPolicy{}, err
	}
	if result.LatestN < 0 {
		return store.SubscriptionPolicy{}, clientError("latestN must not be negative")
	}
	if policy == store.DownloadPolicyLatest && result.LatestN == 0 {
		result.LatestN = store.DefaultDownloadLatestN
	}
	return result, nil
}

// confirmEverything refuses the everything policy unless the caller confirmed
// it, quoting the catalogue size so the client can display it.
func confirmEverything(req request, policy store.SubscriptionPolicy, catalogBytes int64) error {
	if policy.Policy != store.DownloadPolicyEverything || boolVar(req, "confirmEverything") {
		return nil
	}
	return clientError("confirmEverything must be true to download everything: about " + strconv.FormatInt(catalogBytes, 10) + " bytes")
}

func (h *Handler) followFeed(ctx context.Context, scope auth.Scope, req request) (SubscriptionInfo, error) {
	if !canFollowFeeds(scope) {
		return SubscriptionInfo{}, clientError("adult profile required to follow feeds")
	}
	feedURL := strings.TrimSpace(stringVar(req, "feedURL"))
	if feedURL == "" {
		return SubscriptionInfo{}, clientError("feedURL is required")
	}
	if !publicFeedURL(feedURL) {
		return SubscriptionInfo{}, clientError("feedURL must be a public http or https address")
	}
	policy, err := downloadPolicyVariables(req, false)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	library, err := h.followLibrary(ctx, scope, trimmedVariable(req, "libraryID"))
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if h.feedProber == nil {
		return SubscriptionInfo{}, clientError("feed probing unavailable")
	}
	probe, err := h.feedProber.ProbeFeed(ctx, feedURL)
	if err != nil {
		return SubscriptionInfo{}, clientError("feed could not be read: " + string(h.classifyFeedError(err)))
	}
	if err := confirmEverything(req, policy, probe.CatalogBytes); err != nil {
		return SubscriptionInfo{}, err
	}
	root, err := h.followRoot(ctx, library, probe.Title)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	now := time.Now().UTC()
	subscription, created, err := h.store.GetOrCreateSubscription(ctx, store.Subscription{
		LibraryID: library,
		FeedURL:   feedURL,
		Root:      root,
		Title:     strings.TrimSpace(probe.Title),
	}, now)
	if err != nil {
		var validation store.ValidationError
		if errors.As(err, &validation) {
			return SubscriptionInfo{}, clientError("unknown library")
		}
		return SubscriptionInfo{}, err
	}
	// The policy applies only to a new Subscription: following an existing
	// one must not change a policy the manager set.
	if created {
		if err := h.catalogue().SetSubscriptionDownloadPolicy(ctx, subscription.ID, policy); err != nil {
			return SubscriptionInfo{}, err
		}
	}
	if err := h.store.SetFollow(ctx, scope.ProfileID, subscription.ID, true, now); err != nil {
		return SubscriptionInfo{}, err
	}
	if created {
		h.pollSubscription(ctx, subscription.ID)
	}
	info, err := h.subscriptionInfo(ctx, scope, subscription)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	return viewerSafeSubscription(scope, info), nil
}

func (h *Handler) classifyFeedError(err error) store.FeedErrorClass {
	if h.feedErrorClass != nil {
		if class := h.feedErrorClass(err); class != "" {
			return class
		}
	}
	return store.FeedErrorNetwork
}

// followLibrary resolves the target Library: the requested one when it is
// visible, else the single visible Library that can hold feeds.
func (h *Handler) followLibrary(ctx context.Context, scope auth.Scope, requested string) (store.LibraryID, error) {
	if requested != "" {
		visible, err := h.visibleSubscriptionLibrary(ctx, scope, store.LibraryID(requested))
		if err != nil {
			return "", err
		}
		if !visible {
			return "", clientError("unknown library")
		}
		return store.LibraryID(requested), nil
	}
	candidates, err := h.feedLibraries(ctx, scope)
	if err != nil {
		return "", err
	}
	if len(candidates) != 1 {
		return "", clientError("libraryID is required to choose the library for this feed")
	}
	return candidates[0], nil
}

// feedLibraries lists the visible Libraries that can hold feeds: those whose
// plugin declares a subscriptions importer, or, when no plugin does, those
// that already hold a Subscription.
func (h *Handler) feedLibraries(ctx context.Context, scope auth.Scope) ([]store.LibraryID, error) {
	filter, err := h.sealedFilter(ctx, scope.LibraryFilter())
	if err != nil {
		return nil, err
	}
	libraries, err := h.store.Libraries(ctx, filter)
	if err != nil {
		return nil, err
	}
	feedPlugins := map[string]bool{}
	if h.plugins != nil {
		for _, id := range h.plugins.IDs() {
			bundle, ok := h.plugins.Bundle(id)
			if !ok {
				continue
			}
			for _, importer := range bundle.Importers {
				if containsString(importer.Categories, "subscriptions") {
					feedPlugins[string(id)] = true
				}
			}
		}
	}
	var byPlugin, bySubscription []store.LibraryID
	for _, library := range libraries {
		if library.PluginID != nil && feedPlugins[*library.PluginID] {
			byPlugin = append(byPlugin, library.ID)
			continue
		}
		existing, err := h.store.Subscriptions(ctx, library.ID)
		if err != nil {
			return nil, err
		}
		if len(existing) > 0 {
			bySubscription = append(bySubscription, library.ID)
		}
	}
	if len(byPlugin) > 0 {
		return byPlugin, nil
	}
	return bySubscription, nil
}

// followRoot is <library root>/<sanitised title>, suffixed when another
// Subscription in the Library already uses that folder.
func (h *Handler) followRoot(ctx context.Context, library store.LibraryID, title string) (string, error) {
	roots, err := h.store.LibraryRoots(ctx, library)
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", clientError("library has no roots")
	}
	existing, err := h.store.Subscriptions(ctx, library)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(existing))
	for _, subscription := range existing {
		used[subscription.Root] = true
	}
	base := strings.TrimRight(roots[0], "/") + "/" + sanitizeFeedFolder(title)
	root := base
	for n := 2; used[root]; n++ {
		root = base + "-" + strconv.Itoa(n)
	}
	return root, nil
}

// sanitizeFeedFolder makes a feed title a safe folder name: letters and
// digits kept, runs of separators collapsed, anything else a hyphen.
func sanitizeFeedFolder(title string) string {
	var b strings.Builder
	lastSep := false
	for _, r := range title {
		switch {
		case r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastSep = false
		case r == ' ' || r == '-' || r == '_' || r == '.':
			if !lastSep {
				b.WriteRune(r)
				lastSep = true
			}
		default:
			if !lastSep {
				b.WriteRune('-')
				lastSep = true
			}
		}
	}
	name := b.String()
	if len(name) > 120 {
		name = name[:120]
	}
	name = strings.Trim(name, "-. _")
	if name == "" {
		return "feed"
	}
	return name
}

// visibleSubscription loads a Subscription in a Library the caller can see.
// A Subscription elsewhere reads as unknown, with no existence oracle.
func (h *Handler) visibleSubscription(ctx context.Context, scope auth.Scope, id store.SubscriptionID) (store.Subscription, error) {
	if id == "" {
		return store.Subscription{}, clientError("id is required")
	}
	subscription, err := h.store.Subscription(ctx, id)
	if err != nil {
		return store.Subscription{}, clientError("unknown subscription")
	}
	visible, err := h.visibleSubscriptionLibrary(ctx, scope, subscription.LibraryID)
	if err != nil {
		return store.Subscription{}, err
	}
	if !visible {
		return store.Subscription{}, clientError("unknown subscription")
	}
	return subscription, nil
}

const activitySubscriptionDownloadPolicy = "subscription.download_policy"

// setSubscriptionDownloadPolicy changes the policy (manager-gated in
// dispatch, like setSubscriptionRetention).
func (h *Handler) setSubscriptionDownloadPolicy(ctx context.Context, scope auth.Scope, req request) (SubscriptionInfo, error) {
	subscription, err := h.visibleSubscription(ctx, scope, store.SubscriptionID(trimmedVariable(req, "id")))
	if err != nil {
		return SubscriptionInfo{}, err
	}
	policy, err := downloadPolicyVariables(req, true)
	if err != nil {
		return SubscriptionInfo{}, err
	}
	if policy.Policy == store.DownloadPolicyEverything && !boolVar(req, "confirmEverything") {
		current, err := h.withSubscriptionCatalogue(ctx, SubscriptionInfo{}, subscription)
		if err != nil {
			return SubscriptionInfo{}, err
		}
		return SubscriptionInfo{}, confirmEverything(req, policy, current.CatalogBytes)
	}
	if err := h.catalogue().SetSubscriptionDownloadPolicy(ctx, subscription.ID, policy); err != nil {
		if errors.Is(err, store.ErrSubscriptionNotFound) {
			return SubscriptionInfo{}, clientError("unknown subscription")
		}
		return SubscriptionInfo{}, err
	}
	if err := h.logActivity(ctx, scope, activitySubscriptionDownloadPolicy, "subscription", string(subscription.ID), subscription.LibraryID); err != nil {
		return SubscriptionInfo{}, err
	}
	h.pollSubscription(ctx, subscription.ID)
	return h.subscriptionInfo(ctx, scope, subscription)
}

// SubscriptionEntryInfo is one catalogued feed entry. The enclosure URL is
// never carried: playback and download resolve it on the server.
type SubscriptionEntryInfo struct {
	MediaID         string `json:"mediaID"`
	Title           string `json:"title"`
	PublishedAt     string `json:"publishedAt,omitempty"`
	FeedPosition    int    `json:"feedPosition"`
	EnclosureLength int64  `json:"enclosureLength"`
	DownloadState   string `json:"downloadState"`
	// Availability is "downloaded" or "not-downloaded".
	Availability string `json:"availability"`
	// Played reports whether the caller's Profile has played the entry
	// (CORE-2959).
	Played bool `json:"played"`
}

// subscriptionFollow is the caller's follow state and new-entry route for one
// Subscription it can see (CORE-2959): the viewer-safe read of what setFollow
// echoes, so a non-manager can show and change its route.
func (h *Handler) subscriptionFollow(ctx context.Context, scope auth.Scope, req request) (FollowInfo, error) {
	id := store.SubscriptionID(trimmedVariable(req, "subscriptionID"))
	if id == "" {
		return FollowInfo{}, clientError("subscriptionID is required")
	}
	subscription, err := h.visibleSubscription(ctx, scope, id)
	if err != nil {
		return FollowInfo{}, err
	}
	return h.followInfo(ctx, scope, subscription)
}

func (h *Handler) subscriptionEntries(ctx context.Context, scope auth.Scope, req request) ([]SubscriptionEntryInfo, error) {
	id := store.SubscriptionID(trimmedVariable(req, "subscriptionID"))
	if id == "" {
		return nil, clientError("subscriptionID is required")
	}
	subscription, err := h.visibleSubscription(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	entries, err := h.catalogue().SubscriptionEntries(ctx, subscription.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]store.MediaID, len(entries))
	for i, entry := range entries {
		ids[i] = entry.MediaID
	}
	visible, err := h.store.LibraryVisibleMediaItems(ctx, scope.LibraryFilter(), subscription.LibraryID, ids)
	if err != nil {
		return nil, err
	}
	played, err := h.store.PlayedMedia(ctx, scope.ProfileID, ids)
	if err != nil {
		return nil, err
	}
	infos := make([]SubscriptionEntryInfo, 0, len(entries))
	for _, entry := range entries {
		if _, ok := visible[entry.MediaID]; !ok {
			continue
		}
		availability := "not-downloaded"
		if entryDownloaded(entry) {
			availability = "downloaded"
		}
		infos = append(infos, SubscriptionEntryInfo{
			MediaID:         string(entry.MediaID),
			Title:           entry.Title,
			PublishedAt:     formatOptionalTime(entry.PublishedAt),
			FeedPosition:    entry.FeedPosition,
			EnclosureLength: entry.EnclosureLength,
			DownloadState:   string(entry.DownloadState),
			Availability:    availability,
			Played:          played[entry.MediaID],
		})
	}
	return infos, nil
}
