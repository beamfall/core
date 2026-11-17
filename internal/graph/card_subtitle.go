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
		subtitle := map[string]string{}
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
		if subtitle["en"] != "" {
			item.Subtitle = subtitle
		}
	}
	return nil
}
