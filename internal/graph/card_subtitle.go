package graph

import (
	"context"
	"slices"
	"strconv"

	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// cardOrdinalFields are the structural ordinals every Media card carries,
// whatever its plugin's subtitle reads.
var cardOrdinalFields = []string{"sequence", "index"}

// mediaClassField is the conventional text Media field whose values key a
// bundle's MediaClassLabels.
const mediaClassField = "class"

// annotateSubtitles sets each Media item's stored ordinals and renders its
// Library plugin's MediaSubtitle template in every locale the template
// declares (CORE-1226, CORE-2386). It runs after AnnotateMediaClass, which
// supplies each item's Library. Core passes field values through the SDK
// renderer and never reads the template's words. A Library whose plugin is
// not loaded, or declares no template, leaves its items without a subtitle.
func (h *Handler) annotateSubtitles(ctx context.Context, items []store.SourceItem) error {
	templates := map[store.LibraryID][]plugin.LocaleString{}
	names := slices.Clone(cardOrdinalFields)
	var ids []store.MediaID
	for _, item := range items {
		if item.Kind == "entity" || item.LibraryID == "" {
			continue
		}
		ids = append(ids, item.ID)
		if _, seen := templates[item.LibraryID]; seen {
			continue
		}
		var segments []plugin.LocaleString
		if bundle, found := h.libraryBundle(ctx, item.LibraryID); found {
			segments = bundle.MediaSubtitle
		}
		templates[item.LibraryID] = segments
		for _, name := range plugin.MediaSubtitleFields(segments) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	values, err := h.store.MediaCardFieldValues(ctx, ids, names)
	if err != nil {
		return err
	}
	for i := range items {
		item := &items[i]
		fields, ok := values[item.ID]
		if item.Kind == "entity" || !ok {
			continue
		}
		item.Sequence = fields["sequence"].Number
		item.Index = fields["index"].Number
		segments := templates[item.LibraryID]
		if len(segments) == 0 {
			continue
		}
		subtitle := renderSubtitle(segments, fields)
		if len(subtitle) > 0 {
			item.Subtitle = subtitle
		}
	}
	return nil
}

// renderSubtitle renders MediaSubtitle segments in every locale they declare
// from one item's field values. A translation that renders like English is
// left out, and the result is nil when English renders nothing.
func renderSubtitle(segments []plugin.LocaleString, fields map[string]store.MediaField) plugin.LocaleString {
	if len(segments) == 0 {
		return nil
	}
	lookup := func(name string) (string, bool) {
		field, ok := fields[name]
		if !ok {
			return "", false
		}
		if field.Type == store.EntityFieldNumber {
			return strconv.FormatFloat(field.Number, 'f', -1, 64), true
		}
		return field.Text, field.Text != ""
	}
	subtitle := plugin.LocaleString{}
	for _, locale := range plugin.MediaSubtitleLocales(segments) {
		if text := plugin.RenderMediaSubtitle(segments, locale, lookup); text != "" {
			subtitle[locale] = text
		}
	}
	// A translation that renders like English adds nothing on the wire.
	for locale, text := range subtitle {
		if locale != "en" && text == subtitle["en"] {
			delete(subtitle, locale)
		}
	}
	if subtitle["en"] == "" {
		return nil
	}
	return subtitle
}

// describeOrderedMedia fills a media(id) read's ordinals, the headline its
// plugin's ordinal-free MediaSubtitle segments render, and the plugin's label
// for its class (CORE-2469). Core still never reads the template's words: a
// segment is kept or dropped only by the placeholders it names. A failed read
// leaves the fields absent; they decorate the read, never gate it.
func (h *Handler) describeOrderedMedia(ctx context.Context, media store.Media, info *MediaInfo) {
	bundle, found := h.libraryBundle(ctx, media.LibraryID)
	if !found {
		return
	}
	var segments []plugin.LocaleString
	for _, segment := range bundle.MediaSubtitle {
		if !namesOrdinal(segment) {
			segments = append(segments, segment)
		}
	}
	names := append(slices.Clone(cardOrdinalFields), mediaClassField)
	for _, name := range plugin.MediaSubtitleFields(segments) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	values, err := h.store.MediaCardFieldValues(ctx, []store.MediaID{media.ID}, names)
	if err != nil {
		return
	}
	fields := values[media.ID]
	if field, ok := fields["sequence"]; ok {
		info.Sequence = &field.Number
	}
	if field, ok := fields["index"]; ok {
		info.Index = &field.Number
	}
	info.Headline = renderSubtitle(segments, fields)
	// Unmapped classes fall back to the plugin's Media label, as the SDK
	// documents.
	info.ClassLabel = bundle.MediaClassLabels[fields[mediaClassField].Text]
	if len(info.ClassLabel) == 0 {
		info.ClassLabel = bundle.MediaLabel
	}
}

// namesOrdinal reports whether any locale of a MediaSubtitle segment names a
// structural ordinal placeholder.
func namesOrdinal(segment plugin.LocaleString) bool {
	ordinals := plugin.MediaSubtitleOrdinals()
	for _, name := range plugin.MediaSubtitleFields([]plugin.LocaleString{segment}) {
		if slices.Contains(ordinals, name) {
			return true
		}
	}
	return false
}

// MediaSubtitles renders each Media's Library plugin MediaSubtitle in
// English, keyed by Media ID, for reads that carry one plain subtitle such
// as the party rotation (CORE-3048). A Media whose plugin declares no
// template, or whose fields render nothing, is left out. A failed read
// leaves the result short; it decorates, never gates.
func (h *Handler) MediaSubtitles(ctx context.Context, ids []store.MediaID) map[store.MediaID]string {
	out := map[store.MediaID]string{}
	if h.store == nil {
		return out
	}
	items := make([]store.SourceItem, 0, len(ids))
	for _, id := range ids {
		libraryID, err := h.store.MediaLibraryID(ctx, id)
		if err != nil {
			continue
		}
		items = append(items, store.SourceItem{ID: id, Kind: "media", LibraryID: libraryID})
	}
	if err := h.annotateSubtitles(ctx, items); err != nil {
		return out
	}
	for _, item := range items {
		if text := item.Subtitle["en"]; text != "" {
			out[item.ID] = text
		}
	}
	return out
}
