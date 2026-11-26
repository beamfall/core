package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/beamfall/core/internal/artwork"
	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// maxEpisodeArtworkPerPoll bounds the artwork fetches one poll spends on
// episodes; the rest are picked up by later polls.
const maxEpisodeArtworkPerPoll = 40

// showStore is the store surface the show publisher needs; *store.Store
// satisfies it.
type showStore interface {
	LibraryPluginID(ctx context.Context, id store.LibraryID) (string, error)
	UpsertEntity(ctx context.Context, entity store.Entity) error
	UpsertEntityRelation(ctx context.Context, relation store.EntityRelation) error
	SubscriptionEntries(ctx context.Context, id store.SubscriptionID) ([]store.SubscriptionEntry, error)
}

type showArtwork interface {
	HasEntityPoster(id store.EntityID) bool
	ImportEntityPoster(ctx context.Context, id store.EntityID, data []byte) error
	HasPoster(id store.MediaID) bool
	ImportPoster(ctx context.Context, id store.MediaID, data []byte) error
	HasArtwork(id store.MediaID, slot string) bool
	ImportArtwork(ctx context.Context, id store.MediaID, slot string, data []byte) error
}

type showFetcher interface {
	FetchResource(ctx context.Context, rawURL string) ([]byte, error)
}

// subscriptionShows publishes one container Entity per feed Subscription
// (CORE-3524): the outermost container kind of the Library's plugin that holds
// Media through a single-holder relation, titled by the feed, with the channel
// art as its poster and the feed's episodes as the held Media. Episodes
// without their own art inherit the show's. Kind and relation come from the
// plugin bundle, so core names no domain vocabulary; a plugin declaring no
// such kind gets no show.
type subscriptionShows struct {
	store   showStore
	bundles interface {
		Bundle(id plugin.ID) (plugin.Bundle, bool)
	}
	artwork showArtwork
	fetcher showFetcher
	logger  *slog.Logger
}

func newSubscriptionShows(st showStore, bundles interface {
	Bundle(id plugin.ID) (plugin.Bundle, bool)
}, art showArtwork, fetcher showFetcher, logger *slog.Logger) *subscriptionShows {
	return &subscriptionShows{store: st, bundles: bundles, artwork: art, fetcher: fetcher, logger: logger}
}

// feedContainerKind finds the container kind and holder relation a feed's
// episodes hang off, or "" when the bundle declares none.
func feedContainerKind(bundle plugin.Bundle) (kind plugin.EntityKind, relation string, ok bool) {
	outermost := identify.OutermostContainerKinds(bundle)
	for _, candidate := range bundle.EntityKinds {
		if !outermost[candidate.Name] {
			continue
		}
		for _, rel := range candidate.Relations {
			if rel.ToKind == plugin.MediaKindName && rel.Cardinality == "one" {
				return candidate, rel.Name, true
			}
		}
	}
	return plugin.EntityKind{}, "", false
}

func showEntityID(library store.LibraryID, kind string, id store.SubscriptionID) store.EntityID {
	return identify.EntityIDForName(library, kind, "subscription:"+string(id))
}

func (p *subscriptionShows) PublishShow(ctx context.Context, subscription store.Subscription, document scrape.FeedDocument, _ []scrape.FeedEntry) error {
	pluginID, err := p.store.LibraryPluginID(ctx, subscription.LibraryID)
	if err != nil || pluginID == "" {
		return err
	}
	bundle, ok := p.bundles.Bundle(plugin.ID(pluginID))
	if !ok {
		return nil
	}
	kind, relation, ok := feedContainerKind(bundle)
	if !ok {
		return nil
	}
	title := strings.TrimSpace(document.Title)
	if title == "" {
		title = strings.TrimSpace(subscription.Title)
	}
	if title == "" {
		return nil
	}
	id := showEntityID(subscription.LibraryID, kind.Name, subscription.ID)
	entity := store.Entity{ID: id, LibraryID: subscription.LibraryID, Kind: kind.Name, Title: title}
	declared := map[string]string{}
	for _, field := range kind.Fields {
		declared[field.Name] = field.Type
	}
	addText := func(name, value string) {
		value = strings.TrimSpace(value)
		if value == "" || declared[name] == "" {
			return
		}
		fieldType := store.EntityFieldText
		if declared[name] == "url" {
			fieldType = store.EntityFieldURL
		}
		entity.Fields = append(entity.Fields, store.EntityField{Name: name, Type: fieldType, Text: value})
	}
	addText("summary", document.Description)
	addText("website", document.Link)
	addText("publisher", document.Author)
	if declared["serial"] != "" && document.Serial {
		entity.Fields = append(entity.Fields, store.EntityField{Name: "serial", Type: store.EntityFieldFlag, Flag: true})
	}
	if err := p.store.UpsertEntity(ctx, entity); err != nil {
		return fmt.Errorf("upsert show: %w", err)
	}
	entries, err := p.store.SubscriptionEntries(ctx, subscription.ID)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := p.store.UpsertEntityRelation(ctx, store.EntityRelation{
			FromEntityID: id, Name: relation, ToMediaID: entry.MediaID, Cardinality: "one",
		}); err != nil {
			return fmt.Errorf("link episode: %w", err)
		}
	}
	p.applyArtwork(ctx, id, document, entries)
	return nil
}

// applyArtwork is best-effort: a failed fetch is logged and the next poll
// retries, because artwork already present is never refetched.
func (p *subscriptionShows) applyArtwork(ctx context.Context, id store.EntityID, document scrape.FeedDocument, entries []store.SubscriptionEntry) {
	if p.artwork == nil || p.fetcher == nil {
		return
	}
	var showArt []byte
	if url := document.ArtworkURL; url != "" && !p.artwork.HasEntityPoster(id) {
		data, err := p.fetcher.FetchResource(ctx, url)
		if err == nil {
			err = p.artwork.ImportEntityPoster(ctx, id, data)
		}
		if err != nil {
			p.logger.Debug("show artwork failed", "entity", string(id), "error", err.Error())
		} else {
			showArt = data
		}
	}
	itemArt := map[string]string{}
	for _, entry := range document.Entries {
		if entry.ArtworkURL != "" {
			itemArt[entry.GUID] = entry.ArtworkURL
		}
	}
	pending := make([]store.SubscriptionEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.MediaID != "" && !p.artwork.HasPoster(entry.MediaID) {
			pending = append(pending, entry)
		}
	}
	// Newest episodes first: they are the ones on the shelves.
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].PublishedAt.After(pending[j].PublishedAt) })
	if showArt == nil && document.ArtworkURL != "" && len(pending) > 0 {
		if data, err := p.fetcher.FetchResource(ctx, document.ArtworkURL); err == nil {
			showArt = data
		}
	}
	fetched := 0
	for _, entry := range pending {
		data := showArt
		own := false
		if url := itemArt[entry.GUID]; url != "" && url != document.ArtworkURL {
			if fetched >= maxEpisodeArtworkPerPoll {
				continue
			}
			fetched++
			if fetchedData, err := p.fetcher.FetchResource(ctx, url); err == nil {
				data, own = fetchedData, true
			}
		}
		if len(data) == 0 {
			continue
		}
		err := p.artwork.ImportPoster(ctx, entry.MediaID, data)
		if err == nil && own && !p.artwork.HasArtwork(entry.MediaID, artwork.SlotThumb) {
			err = p.artwork.ImportArtwork(ctx, entry.MediaID, artwork.SlotThumb, data)
		}
		var locked *artwork.SlotLockedError
		if err != nil && !errors.As(err, &locked) {
			p.logger.Debug("episode artwork failed", "error", err.Error())
		}
	}
}
