package identify

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
)

// EnrichmentJobKind is the durable entity enrichment pass (CORE-2884): every
// active EntityEnrichment over the shared catalog's due Entities. It is
// network-bound, paced by each scraper's MinRequestIntervalMS.
const EnrichmentJobKind store.JobKind = "entity-enrichment"

// EnrichmentRetryAfter is how long an unmatched Entity waits before the job
// looks it up again. A matched Entity is not looked up again.
const EnrichmentRetryAfter = 30 * 24 * time.Hour

// EnrichmentTransientDelay is how long the job waits after a provider reports
// rate limiting, a server error or a network failure before it resumes.
const EnrichmentTransientDelay = 2 * time.Minute

// EnrichmentJobProgressMessage is the progress message the job reports.
const EnrichmentJobProgressMessage = "Enriching catalog"

// enrichmentBatchSize bounds the Entities looked up per claim, so the job
// checkpoints and yields to the scheduler every minute or so at a one-second
// provider pace.
const enrichmentBatchSize = 20

// enrichmentMemberPage bounds one read of an Entity's member Media.
const enrichmentMemberPage = 200

// EnrichmentSubject is what an enrichment's callbacks see of one Entity: its
// title, its text fields and the titles of the Entities its context relation
// points at.
type EnrichmentSubject struct {
	Title   string
	Fields  map[string]string
	Context []string
}

// EntityEnrichment is a plugin's per-Entity lookup (CORE-2884): Entities of
// Kind are searched on Spec, the picked candidate is looked up, and the names
// it yields become TargetKind Entities related to each of the Entity's member
// Media (reached through MediaRelation) under Relation. Rows of other relation
// names on those Media are never touched.
type EntityEnrichment struct {
	PluginID        string
	Key             string
	Kind            string
	MediaRelation   string
	ContextRelation string
	TargetKind      string
	Relation        string
	Spec            plugin.ScraperSpec
	// Query builds the search query for one Entity; "" means the Entity
	// cannot be looked up and is recorded as unmatched.
	Query func(EnrichmentSubject) string
	// Pick returns the search candidate to look up, or false for none.
	Pick func(EnrichmentSubject, []scrape.Result) (string, bool)
	// Names returns the target Entity titles, in display order, from the
	// looked-up document.
	Names func(scrape.Result) []string
}

// RecordKey is the name the enrichment's per-Entity checks are stored under.
func (e EntityEnrichment) RecordKey() string { return e.PluginID + "/" + e.Key }

// EnrichmentStore is the store surface the enrichment job reads and writes;
// *store.Store satisfies it.
type EnrichmentStore interface {
	EntitiesDueForEnrichment(ctx context.Context, pluginID, kind, enrichment string, after store.EntityID, retryBefore time.Time, limit int) ([]store.EntityRef, error)
	RecordEntityEnrichment(ctx context.Context, id store.EntityID, enrichment string, matched bool, at time.Time) error
	Entity(ctx context.Context, id store.EntityID, filter store.LibraryFilter) (store.Entity, error)
	RelatedEntities(ctx context.Context, entityID store.EntityID, relationName string, filter store.LibraryFilter) ([]store.RelatedEntity, error)
	LiveFoldEqualEntities(ctx context.Context, libraryID store.LibraryID, kind, title, scopeRelation string, scopeTarget store.EntityID) ([]store.Entity, error)
	EnsureEntity(ctx context.Context, entity store.Entity) error
	EntityMemberMediaIDs(ctx context.Context, id store.EntityID, relation string, offset, limit int) ([]store.MediaID, error)
	ReplaceMediaRelations(ctx context.Context, mediaID store.MediaID, relations []store.EntityRelation) error
	UpdateJobCheckpoint(ctx context.Context, id store.JobID, leaseOwner string, checkpoint []byte, progress store.JobProgress, now time.Time) error
	RenewJobLease(ctx context.Context, id store.JobID, leaseOwner string, now time.Time) (time.Time, error)
}

type enrichmentCheckpoint struct {
	Version int    `json:"version"`
	Index   int    `json:"index"`
	LastID  string `json:"last_id,omitempty"`
	Checked int    `json:"checked"`
	Matched int    `json:"matched"`
	Failed  int    `json:"failed"`
}

// NewEnrichmentJob builds the enrichment pass. The dedupe key is fixed, so a
// request while one pass is queued, running or yielded returns that pass.
func NewEnrichmentJob(createdAt time.Time) store.Job {
	return store.Job{
		Kind:      EnrichmentJobKind,
		Class:     store.JobClassBackground,
		DedupeKey: string(EnrichmentJobKind),
		Payload:   []byte(`{"version":1}`),
		CreatedAt: createdAt,
	}
}

// serverFilter reads the shared catalog as the server.
var serverFilter = store.LibraryFilter{Revealed: true, AllLibraries: true}

// EnrichmentHandler returns the handler for EnrichmentJobKind. Each claim
// looks up a batch of due Entities, enrichment by enrichment in id order, and
// records each check. A transient provider error ends the claim with the
// checkpoint kept and the job requeued after EnrichmentTransientDelay; any
// other per-Entity error is counted and the Entity is left for the next pass.
func EnrichmentHandler(st EnrichmentStore, engine Engine, enrichments []EntityEnrichment, logger *slog.Logger) jobs.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, job store.Job) (jobs.Result, error) {
		var checkpoint enrichmentCheckpoint
		if len(job.Checkpoint) > 0 {
			if err := json.Unmarshal(job.Checkpoint, &checkpoint); err != nil {
				return jobs.Result{}, err
			}
		}
		now := time.Now().UTC()
		retryBefore := now.Add(-EnrichmentRetryAfter)
		done := 0
		more := false
		var runAfter time.Time
	walk:
		for checkpoint.Index < len(enrichments) {
			enrichment := enrichments[checkpoint.Index]
			limit := enrichmentBatchSize - done + 1
			refs, err := st.EntitiesDueForEnrichment(ctx, enrichment.PluginID, enrichment.Kind, enrichment.RecordKey(),
				store.EntityID(checkpoint.LastID), retryBefore, limit)
			if err != nil {
				return jobs.Result{}, err
			}
			for _, ref := range refs {
				if done >= enrichmentBatchSize || ctx.Err() != nil {
					more = true
					break walk
				}
				if _, err := st.RenewJobLease(ctx, job.ID, job.LeaseOwner, time.Now().UTC()); err != nil {
					if ctx.Err() != nil {
						more = true
						break walk
					}
					return jobs.Result{}, err
				}
				matched, err := enrichEntity(ctx, st, engine, enrichment, ref)
				if err != nil && ctx.Err() != nil {
					more = true
					break walk
				}
				if err != nil && scrape.IsTransient(err) {
					logger.InfoContext(ctx, "entity enrichment paused on provider error", "plugin", enrichment.PluginID, "key", enrichment.Key, "error", err)
					more = true
					runAfter = time.Now().UTC().Add(EnrichmentTransientDelay)
					break walk
				}
				switch {
				case err != nil:
					checkpoint.Failed++
					logger.DebugContext(ctx, "entity enrichment failed", "entity", ref.ID, "key", enrichment.Key, "error", err)
				default:
					checkpoint.Checked++
					if matched {
						checkpoint.Matched++
					}
				}
				checkpoint.LastID = string(ref.ID)
				done++
			}
			if len(refs) < limit {
				checkpoint.Index++
				checkpoint.LastID = ""
			}
			if done >= enrichmentBatchSize {
				more = checkpoint.Index < len(enrichments)
				break
			}
		}
		checkpoint.Version = 1
		data, err := json.Marshal(checkpoint)
		if err != nil {
			return jobs.Result{}, err
		}
		progress := store.JobProgress{
			Current: int64(checkpoint.Checked + checkpoint.Failed),
			Unit:    "items",
			Message: EnrichmentJobProgressMessage,
		}
		// The checkpoint write must land even when shutdown cancelled ctx; a
		// cancel still refuses it through the lost lease.
		if err := st.UpdateJobCheckpoint(context.WithoutCancel(ctx), job.ID, job.LeaseOwner, data, progress, time.Now().UTC()); err != nil {
			return jobs.Result{}, err
		}
		if more {
			return jobs.Result{Checkpoint: data, RunAfter: runAfter}, jobs.ErrYielded
		}
		return jobs.Result{}, nil
	}
}

// enrichEntity looks one Entity up and writes its target relations, recording
// the check. It reports whether names were written. An Entity that vanished
// since the page was read is skipped without a record.
func enrichEntity(ctx context.Context, st EnrichmentStore, engine Engine, enrichment EntityEnrichment, ref store.EntityRef) (bool, error) {
	entity, err := st.Entity(ctx, ref.ID, serverFilter)
	if store.IsEntityNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	subject := EnrichmentSubject{Title: entity.Title, Fields: map[string]string{}}
	for _, field := range entity.Fields {
		if field.Text != "" {
			subject.Fields[field.Name] = field.Text
		}
	}
	if enrichment.ContextRelation != "" {
		related, err := st.RelatedEntities(ctx, ref.ID, enrichment.ContextRelation, serverFilter)
		if err != nil {
			return false, err
		}
		for _, r := range related {
			subject.Context = append(subject.Context, r.Entity.Title)
		}
	}
	names, err := lookupEnrichmentNames(ctx, engine, enrichment, subject)
	if err != nil {
		return false, err
	}
	if len(names) > 0 {
		if err := writeEnrichment(ctx, st, enrichment, ref, names); err != nil {
			return false, err
		}
	}
	matched := len(names) > 0
	return matched, st.RecordEntityEnrichment(ctx, ref.ID, enrichment.RecordKey(), matched, time.Now().UTC())
}

func lookupEnrichmentNames(ctx context.Context, engine Engine, enrichment EntityEnrichment, subject EnrichmentSubject) ([]string, error) {
	query := strings.TrimSpace(enrichment.Query(subject))
	if query == "" {
		return nil, nil
	}
	candidates, err := engine.Search(ctx, enrichment.Spec, query)
	if err != nil {
		return nil, err
	}
	id, ok := enrichment.Pick(subject, candidates)
	if !ok || id == "" {
		return nil, nil
	}
	result, err := engine.Lookup(ctx, enrichment.Spec, id)
	if err != nil {
		return nil, err
	}
	var names []string
	seen := map[string]bool{}
	for _, name := range enrichment.Names(result) {
		name = strings.TrimSpace(name)
		key := store.FoldEntityTitle(name)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, name)
	}
	return names, nil
}

// writeEnrichment resolves each name to a target Entity in the Entity's
// Library, reusing a live fold-equal Entity before creating one, and replaces
// the enrichment relation of every member Media.
func writeEnrichment(ctx context.Context, st EnrichmentStore, enrichment EntityEnrichment, ref store.EntityRef, names []string) error {
	targets := make([]store.EntityID, 0, len(names))
	for _, name := range names {
		existing, err := st.LiveFoldEqualEntities(ctx, ref.LibraryID, enrichment.TargetKind, name, "", "")
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			targets = append(targets, existing[0].ID)
			continue
		}
		id := EntityIDForName(ref.LibraryID, enrichment.TargetKind, name)
		if err := st.EnsureEntity(ctx, store.Entity{ID: id, LibraryID: ref.LibraryID, Kind: enrichment.TargetKind, Title: name}); err != nil {
			return err
		}
		targets = append(targets, id)
	}
	for offset := 0; ; offset += enrichmentMemberPage {
		members, err := st.EntityMemberMediaIDs(ctx, ref.ID, enrichment.MediaRelation, offset, enrichmentMemberPage)
		if err != nil {
			return err
		}
		for _, media := range members {
			rows := make([]store.EntityRelation, 0, len(targets))
			for i, target := range targets {
				order := i
				rows = append(rows, store.EntityRelation{
					FromEntityID: target, Name: enrichment.Relation, ToMediaID: media,
					Cardinality: "many", SortOrder: &order,
				})
			}
			if err := st.ReplaceMediaRelations(ctx, media, rows); err != nil {
				return err
			}
		}
		if len(members) < enrichmentMemberPage {
			return nil
		}
	}
}

// errEnrichmentInvalid marks an EntityEnrichment missing a required part.
var errEnrichmentInvalid = errors.New("identify: entity enrichment is incomplete")

// Validate reports an enrichment missing a name, relation or callback the job
// needs. Declaration checks against the plugin bundle live in the composition
// root.
func (e EntityEnrichment) Validate() error {
	if e.PluginID == "" || e.Key == "" || e.Kind == "" || e.MediaRelation == "" || e.TargetKind == "" ||
		e.Relation == "" || e.Spec.ID == "" || e.Query == nil || e.Pick == nil || e.Names == nil {
		return errEnrichmentInvalid
	}
	return nil
}
