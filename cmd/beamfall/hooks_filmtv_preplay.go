package main

import (
	"context"

	"github.com/beamfall/core/internal/app"
	"github.com/beamfall/core/internal/graph"
	"github.com/beamfall/core/internal/store"
	filmtv "github.com/beamfall/plugin-filmtv"
)

// filmtvClassField is the Film & TV bundle's Media field naming what a Media
// item is; the plugin's pre-play policy reads it and core never interprets it.
const filmtvClassField = "class"

// filmtvPrePlayHook contributes the Film & TV pre-play policy (CORE-1701).
func filmtvPrePlayHook() *app.PrePlayPolicyHook {
	return &app.PrePlayPolicyHook{
		PluginID: filmtv.PluginID,
		New: func(st *store.Store) graph.PrePlayPolicy {
			return filmtvPrePlayPolicy{store: st}
		},
	}
}

// filmtvPrePlayPolicy adapts filmtv.PlanPrePlay to graph.PrePlayPolicy. Every
// read goes through the requesting Profile's filter, so the plugin only ever
// sees Media that Profile may play.
type filmtvPrePlayPolicy struct {
	store *store.Store
}

func (p filmtvPrePlayPolicy) PlanPrePlay(ctx context.Context, req graph.PrePlayRequest) (graph.PrePlayPlan, error) {
	plan, err := filmtv.PlanPrePlay(ctx, filmtvPrePlayReader{store: p.store, req: req}, filmtv.PrePlayRequest{
		FeatureID: string(req.FeatureID),
		Count:     req.Count,
		Seed:      req.Seed,
	})
	if err != nil {
		return graph.PrePlayPlan{}, err
	}
	ids := make([]store.MediaID, 0, len(plan.MediaIDs))
	for _, id := range plan.MediaIDs {
		ids = append(ids, store.MediaID(id))
	}
	return graph.PrePlayPlan{Eligible: plan.Eligible, MediaIDs: ids, Label: plan.Label}, nil
}

type filmtvPrePlayReader struct {
	store *store.Store
	req   graph.PrePlayRequest
}

func (r filmtvPrePlayReader) visible(ctx context.Context, ids []string) (map[store.MediaID]store.Media, error) {
	mediaIDs := make([]store.MediaID, 0, len(ids))
	for _, id := range ids {
		mediaIDs = append(mediaIDs, store.MediaID(id))
	}
	return r.store.MediaByIDs(ctx, mediaIDs, r.req.Filter)
}

func (r filmtvPrePlayReader) MediaClasses(ctx context.Context, ids []string) (map[string]string, error) {
	visible, err := r.visible(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(visible))
	for id := range visible {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fields, err := r.store.MediaFields(ctx, id, r.req.Filter)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			if field.Name == filmtvClassField {
				out[string(id)] = field.Text
			}
		}
	}
	return out, nil
}

func (r filmtvPrePlayReader) Extras(ctx context.Context, featureID string) ([]filmtv.PrePlayExtra, error) {
	links, err := r.store.MediaExtraLinks(ctx, store.MediaID(featureID), r.req.Filter)
	if err != nil {
		return nil, err
	}
	out := make([]filmtv.PrePlayExtra, 0, len(links))
	for _, link := range links {
		out = append(out, filmtv.PrePlayExtra{MediaID: string(link.ExtraID), ParentID: string(link.ParentID), Path: link.Path})
	}
	return out, nil
}

func (r filmtvPrePlayReader) Watched(ctx context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	if r.req.ProfileID == "" {
		return out, nil
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		progress, ok, err := r.store.PlaybackProgressFor(ctx, r.req.ProfileID, store.MediaID(id))
		if err != nil {
			return nil, err
		}
		out[id] = ok && progress.Watched
	}
	return out, nil
}
