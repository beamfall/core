package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/beamfall/core/internal/identify"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/store"
)

// EntityEnrichment is a plugin wiring's per-Entity provider lookup
// (CORE-2884); the enrichment job applies the active ones.
type EntityEnrichment = identify.EntityEnrichment

// EnrichmentSubject is the Entity an enrichment's Query and Pick read.
type EnrichmentSubject = identify.EnrichmentSubject

// activeEntityEnrichments keeps the enrichments of activated plugins whose
// kinds, relations and scraper the plugin's bundle really declares, in
// declared order. An enrichment may run a variant of a declared scraper (other
// templates on the same endpoint), sharing its ID and so its request pace. One
// naming something undeclared is a wiring fault: it is dropped and logged
// rather than left to write relations no reader expects.
func activeEntityEnrichments(registry *plugin.Registry, enrichments []EntityEnrichment, logger *slog.Logger) []EntityEnrichment {
	if registry == nil {
		return nil
	}
	var active []EntityEnrichment
	for _, enrichment := range enrichments {
		bundle, ok := registry.Bundle(plugin.ID(enrichment.PluginID))
		if !ok {
			continue
		}
		if reason := entityEnrichmentFault(bundle, enrichment); reason != "" {
			if logger != nil {
				logger.Warn("entity enrichment dropped", "plugin", enrichment.PluginID, "key", enrichment.Key, "reason", reason)
			}
			continue
		}
		active = append(active, enrichment)
	}
	return active
}

// entityEnrichmentFault reports why the enrichment cannot run against this
// bundle, or "" when it can.
func entityEnrichmentFault(bundle plugin.Bundle, enrichment EntityEnrichment) string {
	if err := enrichment.Validate(); err != nil {
		return "incomplete enrichment"
	}
	kinds := declaredEntityFieldTypes(bundle)
	for _, kind := range []string{enrichment.Kind, enrichment.TargetKind} {
		if _, declared := kinds[kind]; !declared {
			return "undeclared entity kind " + kind
		}
	}
	specs := declaredRelationSpecs(bundle)
	relations := []struct{ from, name, to string }{
		{enrichment.Kind, enrichment.MediaRelation, plugin.MediaKindName},
		{enrichment.TargetKind, enrichment.Relation, plugin.MediaKindName},
	}
	if enrichment.ContextRelation != "" {
		relations = append(relations, struct{ from, name, to string }{enrichment.Kind, enrichment.ContextRelation, ""})
	}
	for _, relation := range relations {
		spec, declared := specs[relation.from+"\x00"+relation.name]
		if !declared || (relation.to != "" && spec.ToKind != relation.to) {
			return "relation " + relation.name + " from " + relation.from + " is not declared"
		}
	}
	for _, scraper := range bundle.Scrapers {
		if scraper.ID == enrichment.Spec.ID && scraper.Endpoint == enrichment.Spec.Endpoint {
			return ""
		}
	}
	return "scraper " + enrichment.Spec.ID + " is not declared on its endpoint"
}

// enqueueEntityEnrichment schedules the enrichment pass when any enrichment is
// active; failure to enqueue is logged, never fatal — the next scan or restart
// re-arms it.
func enqueueEntityEnrichment(ctx context.Context, st *store.Store, logger *slog.Logger, wake *jobWake, enrichments []EntityEnrichment) {
	if len(enrichments) == 0 {
		return
	}
	if _, err := enqueueAndWake(ctx, st, wake, identify.NewEnrichmentJob(time.Now().UTC())); err != nil {
		logger.Warn("entity enrichment enqueue failed", "error", err)
	}
}
