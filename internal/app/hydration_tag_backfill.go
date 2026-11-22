package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/serverrole"
	"github.com/beamfall/core/internal/store"
)

const hydrationTagBackfillKind store.JobKind = "hydration-tag-backfill"

type metadataTagProber interface {
	ProbeMetadataTags(ctx context.Context, path string, keys []string) (map[string]string, error)
}

func queueHydrationTagBackfills(ctx context.Context, st *store.Store, hydrators map[string]scan.MetadataHydrator, wake *jobWake, logger *slog.Logger) {
	libraries, err := st.Libraries(ctx, metadataLibraryFilter)
	if err != nil {
		logger.Warn("tag backfill could not list libraries", "error", err)
		return
	}
	for _, library := range libraries {
		if library.PluginID == nil {
			continue
		}
		adapter, _ := hydrators[*library.PluginID].(*hydrationAdapter)
		if adapter == nil || adapter.tag.Field == "" {
			continue
		}
		key := "tag/" + adapter.tag.Field
		checked, err := st.HydrationBackfillChecked(ctx, library.ID, key)
		if errors.Is(err, serverrole.ErrAttachedAuthorityRefused) {
			return
		}
		if err == nil && !checked {
			err = queueHydrationTagBackfill(ctx, st, adapter, library.ID, key, wake)
		}
		if err != nil && !errors.Is(err, store.ErrEncryptedLibraryDBDisabled) {
			logger.Warn("tag backfill could not be queued", "library", library.ID, "error", err)
		}
	}
}

func queueHydrationTagBackfill(ctx context.Context, st *store.Store, adapter *hydrationAdapter, libraryID store.LibraryID, key string, wake *jobWake) error {
	candidates, err := st.HydrationTagCandidates(ctx, libraryID, adapter.capability.MediaClasses, adapter.tag.Field)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return st.MarkHydrationBackfillChecked(ctx, libraryID, key, time.Now().UTC())
	}
	payload, err := json.Marshal(libraryID)
	if err != nil {
		return err
	}
	_, err = enqueueAndWake(ctx, st, wake, store.Job{
		Kind: hydrationTagBackfillKind, Class: store.JobClassBackground,
		DedupeKey: string(libraryID) + ":" + key, Payload: payload,
		CreatedAt: time.Now().UTC(),
	})
	return err
}

func hydrationTagBackfillHandler(st *store.Store, hydrators map[string]scan.MetadataHydrator, prober metadataTagProber, logger *slog.Logger) jobs.Handler {
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var libraryID store.LibraryID
		if err := json.Unmarshal(job.Payload, &libraryID); err != nil {
			return jobs.Result{}, err
		}
		pluginID, err := st.LibraryPluginID(ctx, libraryID)
		if err != nil {
			return jobs.Result{}, err
		}
		adapter, _ := hydrators[pluginID].(*hydrationAdapter)
		if adapter == nil || adapter.tag.Field == "" {
			return jobs.Result{}, nil
		}
		candidates, err := st.HydrationTagCandidates(ctx, libraryID, adapter.capability.MediaClasses, adapter.tag.Field)
		if err != nil {
			return jobs.Result{}, err
		}
		var failed int
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return jobs.Result{}, err
			}
			value := candidate.PersistedValue
			if value == "" {
				probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				tags, err := prober.ProbeMetadataTags(probeCtx, candidate.Path, adapter.tag.Keys)
				cancel()
				if err != nil {
					logger.Warn("tag backfill could not read file", "media", candidate.MediaID, "error", err)
					failed++
					continue
				}
				value = adapter.tag.Read(tags)
			}
			// An empty value records a successful probe of an untagged file.
			// Absence-only prevents overwriting a tag written by a scan.
			field := store.MediaField{Name: adapter.tag.Field, Type: store.EntityFieldText, Text: value}
			if err := st.InsertMediaFieldsIfAbsent(ctx, candidate.MediaID, []store.MediaField{field}); err != nil {
				return jobs.Result{}, err
			}
		}
		if failed > 0 {
			return jobs.Result{}, fmt.Errorf("backfill could not read tags for %d Media", failed)
		}
		return jobs.Result{}, st.MarkHydrationBackfillChecked(ctx, libraryID, "tag/"+adapter.tag.Field, time.Now().UTC())
	}
}
