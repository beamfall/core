package identify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"testing"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/plugin"
	"github.com/beamfall/core/internal/scrape"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

type enrichmentRecord struct {
	matched bool
	at      time.Time
}

type fakeEnrichmentStore struct {
	entities   map[store.EntityID]store.Entity
	context    map[store.EntityID][]store.RelatedEntity
	members    map[store.EntityID][]store.MediaID
	records    map[string]enrichmentRecord
	relations  map[store.MediaID][]store.EntityRelation
	ensured    []store.Entity
	checkpoint []byte
	progress   store.JobProgress
}

func newFakeEnrichmentStore() *fakeEnrichmentStore {
	return &fakeEnrichmentStore{
		entities:  map[store.EntityID]store.Entity{},
		context:   map[store.EntityID][]store.RelatedEntity{},
		members:   map[store.EntityID][]store.MediaID{},
		records:   map[string]enrichmentRecord{},
		relations: map[store.MediaID][]store.EntityRelation{},
	}
}

func (f *fakeEnrichmentStore) EntitiesDueForEnrichment(_ context.Context, _, kind, enrichment string, after store.EntityID, retryBefore time.Time, limit int) ([]store.EntityRef, error) {
	var ids []string
	for id, entity := range f.entities {
		if entity.Kind != kind || id <= after {
			continue
		}
		if record, ok := f.records[string(id)+"\x00"+enrichment]; ok && (record.matched || !record.at.Before(retryBefore)) {
			continue
		}
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	var refs []store.EntityRef
	for _, id := range ids {
		if len(refs) == limit {
			break
		}
		refs = append(refs, store.EntityRef{ID: store.EntityID(id), LibraryID: f.entities[store.EntityID(id)].LibraryID})
	}
	return refs, nil
}

func (f *fakeEnrichmentStore) RecordEntityEnrichment(_ context.Context, id store.EntityID, enrichment string, matched bool, at time.Time) error {
	f.records[string(id)+"\x00"+enrichment] = enrichmentRecord{matched: matched, at: at}
	return nil
}

func (f *fakeEnrichmentStore) Entity(_ context.Context, id store.EntityID, _ store.LibraryFilter) (store.Entity, error) {
	entity, ok := f.entities[id]
	if !ok {
		return store.Entity{}, errors.New("entity missing")
	}
	return entity, nil
}

func (f *fakeEnrichmentStore) RelatedEntities(_ context.Context, id store.EntityID, _ string, _ store.LibraryFilter) ([]store.RelatedEntity, error) {
	return f.context[id], nil
}

func (f *fakeEnrichmentStore) LiveFoldEqualEntities(_ context.Context, library store.LibraryID, kind, title, _ string, _ store.EntityID) ([]store.Entity, error) {
	var out []store.Entity
	for _, entity := range f.entities {
		if entity.LibraryID == library && entity.Kind == kind && store.FoldEntityTitle(entity.Title) == store.FoldEntityTitle(title) {
			out = append(out, entity)
		}
	}
	return out, nil
}

func (f *fakeEnrichmentStore) EnsureEntity(_ context.Context, entity store.Entity) error {
	f.ensured = append(f.ensured, entity)
	f.entities[entity.ID] = entity
	return nil
}

func (f *fakeEnrichmentStore) EntityMemberMediaIDs(_ context.Context, id store.EntityID, _ string, offset, limit int) ([]store.MediaID, error) {
	members := f.members[id]
	if offset >= len(members) {
		return nil, nil
	}
	end := min(offset+limit, len(members))
	return members[offset:end], nil
}

func (f *fakeEnrichmentStore) ReplaceMediaRelations(_ context.Context, media store.MediaID, rows []store.EntityRelation) error {
	f.relations[media] = rows
	return nil
}

func (f *fakeEnrichmentStore) UpdateJobCheckpoint(_ context.Context, _ store.JobID, _ string, checkpoint []byte, progress store.JobProgress, _ time.Time) error {
	f.checkpoint = checkpoint
	f.progress = progress
	return nil
}

func (f *fakeEnrichmentStore) RenewJobLease(_ context.Context, _ store.JobID, _ string, now time.Time) (time.Time, error) {
	return now.Add(time.Minute), nil
}

type fakeEnrichmentEngine struct {
	searches  []string
	results   map[string][]scrape.Result
	documents map[string]scrape.Result
	lookupErr map[string]error
}

func (f *fakeEnrichmentEngine) Search(_ context.Context, _ plugin.ScraperSpec, query string, _ ...scrape.Variables) ([]scrape.Result, error) {
	f.searches = append(f.searches, query)
	return f.results[query], nil
}

func (f *fakeEnrichmentEngine) Lookup(_ context.Context, _ plugin.ScraperSpec, id string, _ ...scrape.Variables) (scrape.Result, error) {
	if err := f.lookupErr[id]; err != nil {
		return scrape.Result{}, err
	}
	return f.documents[id], nil
}

const testEnrichmentLibrary store.LibraryID = "lib"

func testEnrichment() EntityEnrichment {
	return EntityEnrichment{
		PluginID: "p", Key: "k", Kind: "set", MediaRelation: "set-member", ContextRelation: "set-maker",
		TargetKind: "tag", Relation: "tag-of", Spec: plugin.ScraperSpec{ID: "s"},
		Query: func(subject EnrichmentSubject) string {
			if subject.Fields["skip"] != "" {
				return ""
			}
			if len(subject.Context) > 0 {
				return subject.Title + " by " + subject.Context[0]
			}
			return subject.Title
		},
		Pick: func(subject EnrichmentSubject, candidates []scrape.Result) (string, bool) {
			for _, candidate := range candidates {
				if candidate.Fields["title"] == subject.Title {
					return candidate.ID, true
				}
			}
			return "", false
		},
		Names: func(result scrape.Result) []string {
			var names []string
			for _, entity := range result.Entities {
				names = append(names, entity.Name)
			}
			return names
		},
	}
}

func addEnrichmentSubject(st *fakeEnrichmentStore, engine *fakeEnrichmentEngine, id, title string, names ...string) {
	entityID := store.EntityID(id)
	st.entities[entityID] = store.Entity{ID: entityID, LibraryID: testEnrichmentLibrary, Kind: "set", Title: title}
	st.members[entityID] = []store.MediaID{store.MediaID(id + "-m1"), store.MediaID(id + "-m2")}
	engine.results[title] = []scrape.Result{{ID: "doc-" + id, Fields: map[string]any{"title": title}}}
	var entities []scrape.EntityResult
	for _, name := range names {
		entities = append(entities, scrape.EntityResult{Name: name})
	}
	engine.documents["doc-"+id] = scrape.Result{ID: "doc-" + id, Entities: entities}
}

func newEnrichmentFixture() (*fakeEnrichmentStore, *fakeEnrichmentEngine) {
	return newFakeEnrichmentStore(), &fakeEnrichmentEngine{
		results:   map[string][]scrape.Result{},
		documents: map[string]scrape.Result{},
		lookupErr: map[string]error{},
	}
}

func runEnrichment(t *testing.T, st *fakeEnrichmentStore, engine *fakeEnrichmentEngine, checkpoint []byte) (jobs.Result, error) {
	t.Helper()
	handler := EnrichmentHandler(st, engine, []EntityEnrichment{testEnrichment()}, nil)
	return handler(context.Background(), store.Job{ID: "job", LeaseOwner: "owner", Checkpoint: checkpoint})
}

func TestEnrichmentWritesTargetsToMemberMedia(t *testing.T) {
	st, engine := newEnrichmentFixture()
	addEnrichmentSubject(st, engine, "e1", "First", "Shoegaze", "dream pop", "Shoegaze")
	st.context["e1"] = []store.RelatedEntity{{Entity: store.Entity{Title: "Maker"}}}
	engine.results["First by Maker"] = engine.results["First"]
	existing := store.Entity{ID: "tag-existing", LibraryID: testEnrichmentLibrary, Kind: "tag", Title: "Dream Pop"}
	st.entities[existing.ID] = existing

	if _, err := runEnrichment(t, st, engine, nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(engine.searches) != 1 || engine.searches[0] != "First by Maker" {
		t.Fatalf("searches = %v, want the context-qualified query", engine.searches)
	}
	created := EntityIDForName(testEnrichmentLibrary, "tag", "Shoegaze")
	for _, media := range []store.MediaID{"e1-m1", "e1-m2"} {
		rows := st.relations[media]
		if len(rows) != 2 {
			t.Fatalf("%s relations = %+v, want two deduplicated targets", media, rows)
		}
		for i, want := range []store.EntityID{created, existing.ID} {
			row := rows[i]
			if row.FromEntityID != want || row.Name != "tag-of" || row.ToMediaID != media || row.Cardinality != "many" || row.SortOrder == nil || *row.SortOrder != i {
				t.Errorf("%s row %d = %+v, want target %s at order %d", media, i, row, want, i)
			}
		}
	}
	if len(st.ensured) != 1 || st.ensured[0].ID != created || st.ensured[0].Title != "Shoegaze" {
		t.Errorf("ensured = %+v, want only the new target", st.ensured)
	}
	if record := st.records["e1\x00p/k"]; !record.matched {
		t.Errorf("record = %+v, want matched", record)
	}
	if st.progress.Message != EnrichmentJobProgressMessage || st.progress.Current != 1 {
		t.Errorf("progress = %+v", st.progress)
	}
}

func TestEnrichmentPreservesOperatorRelationAttributes(t *testing.T) {
	st := storetest.Open(t)
	ctx := t.Context()
	enrichment := testEnrichment()
	lib := store.LibraryID("library-local")
	media := store.MediaID("media-alpha")
	filter := store.LibraryFilter{AllLibraries: true}
	subject := store.Entity{ID: "enrichment-subject", LibraryID: lib, Kind: enrichment.Kind, Title: "Subject"}
	for _, entity := range []store.Entity{
		subject,
		{ID: "operator-tag", LibraryID: lib, Kind: enrichment.TargetKind, Title: "Selected"},
		{ID: "stale-tag", LibraryID: lib, Kind: enrichment.TargetKind, Title: "Stale"},
	} {
		if err := st.UpsertEntity(ctx, entity); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: subject.ID, Name: enrichment.MediaRelation, ToMediaID: media,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertEntityRelation(ctx, store.EntityRelation{
		FromEntityID: "stale-tag", Name: enrichment.Relation, ToMediaID: media,
	}); err != nil {
		t.Fatal(err)
	}
	row := store.EntityRelation{FromEntityID: "operator-tag", Name: enrichment.Relation,
		ToMediaID: media, Cardinality: "many", Attributes: map[string]string{"role": "selected"}}
	if _, err := st.SetOperatorMediaRelation(ctx, row, store.Entity{}, filter, store.ActivityEntry{
		OperationID: "operator-tag-assignment", OccurredAt: time.Now(), ProfileID: "profile-default",
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeEnrichment(ctx, st, enrichment, store.EntityRef{ID: subject.ID, LibraryID: lib}, []string{"Selected", "New"}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.EntitiesForMedia(ctx, media, enrichment.Relation, filter)
	if err != nil || len(rows) != 2 || rows[0].Entity.Title != "New" || rows[0].OperatorLocked ||
		rows[1].Entity.ID != "operator-tag" || !rows[1].OperatorLocked || rows[1].Attributes["role"] != "selected" {
		t.Fatalf("enriched relations = %+v, %v", rows, err)
	}
}

func TestEnrichmentRecordsUnmatchedWithoutWriting(t *testing.T) {
	st, engine := newEnrichmentFixture()
	addEnrichmentSubject(st, engine, "e1", "No Pick")
	engine.results["No Pick"] = []scrape.Result{{ID: "x", Fields: map[string]any{"title": "Other"}}}
	addEnrichmentSubject(st, engine, "e2", "Skipped", "Tag")
	st.entities["e2"] = store.Entity{ID: "e2", LibraryID: testEnrichmentLibrary, Kind: "set", Title: "Skipped",
		Fields: []store.EntityField{{Name: "skip", Text: "yes"}}}
	addEnrichmentSubject(st, engine, "e3", "No Names")

	if _, err := runEnrichment(t, st, engine, nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	for _, id := range []string{"e1", "e2", "e3"} {
		record, ok := st.records[id+"\x00p/k"]
		if !ok || record.matched {
			t.Errorf("%s record = %+v (present %v), want unmatched", id, record, ok)
		}
	}
	if len(st.relations) != 0 {
		t.Errorf("relations = %+v, want none", st.relations)
	}
	for _, query := range engine.searches {
		if query == "Skipped" {
			t.Errorf("searched an entity whose query is empty")
		}
	}
	// A recorded miss is not looked up again before the retry window.
	engine.searches = nil
	if _, err := runEnrichment(t, st, engine, nil); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if len(engine.searches) != 0 {
		t.Errorf("second pass searched %v, want nothing due", engine.searches)
	}
}

func TestEnrichmentPausesOnTransientError(t *testing.T) {
	st, engine := newEnrichmentFixture()
	addEnrichmentSubject(st, engine, "e1", "One", "A")
	addEnrichmentSubject(st, engine, "e2", "Two", "B")
	addEnrichmentSubject(st, engine, "e3", "Three", "C")
	engine.lookupErr["doc-e2"] = fmt.Errorf("lookup: %w", &net.OpError{Op: "dial", Err: errors.New("refused")})

	before := time.Now()
	result, err := runEnrichment(t, st, engine, nil)
	if !errors.Is(err, jobs.ErrYielded) {
		t.Fatalf("err = %v, want yielded", err)
	}
	if result.RunAfter.Before(before.Add(EnrichmentTransientDelay)) {
		t.Errorf("RunAfter = %v, want at least %v out", result.RunAfter, EnrichmentTransientDelay)
	}
	if _, ok := st.records["e2\x00p/k"]; ok {
		t.Errorf("recorded the entity the provider failed on")
	}
	if _, ok := st.records["e3\x00p/k"]; ok {
		t.Errorf("continued past a transient error")
	}
	var checkpoint enrichmentCheckpoint
	if err := json.Unmarshal(result.Checkpoint, &checkpoint); err != nil || checkpoint.LastID != "e1" {
		t.Fatalf("checkpoint = %+v (%v), want resume after e1", checkpoint, err)
	}

	delete(engine.lookupErr, "doc-e2")
	if _, err := runEnrichment(t, st, engine, result.Checkpoint); err != nil {
		t.Fatalf("resume: %v", err)
	}
	for _, id := range []string{"e2", "e3"} {
		if !st.records[id+"\x00p/k"].matched {
			t.Errorf("%s not matched after resume", id)
		}
	}
}

func TestEnrichmentCountsOtherErrorsAndContinues(t *testing.T) {
	st, engine := newEnrichmentFixture()
	addEnrichmentSubject(st, engine, "e1", "One", "A")
	addEnrichmentSubject(st, engine, "e2", "Two", "B")
	engine.lookupErr["doc-e1"] = errors.New("decode failed")

	if _, err := runEnrichment(t, st, engine, nil); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if _, ok := st.records["e1\x00p/k"]; ok {
		t.Errorf("recorded a failed lookup")
	}
	if !st.records["e2\x00p/k"].matched {
		t.Errorf("did not continue past a failed lookup")
	}
	var checkpoint enrichmentCheckpoint
	if err := json.Unmarshal(st.checkpoint, &checkpoint); err != nil || checkpoint.Failed != 1 || checkpoint.Matched != 1 {
		t.Errorf("checkpoint = %+v (%v), want one failed and one matched", checkpoint, err)
	}
}

func TestEnrichmentYieldsBetweenBatches(t *testing.T) {
	st, engine := newEnrichmentFixture()
	total := enrichmentBatchSize + 5
	for i := range total {
		addEnrichmentSubject(st, engine, fmt.Sprintf("e%02d", i), fmt.Sprintf("Title %02d", i), "A")
	}
	result, err := runEnrichment(t, st, engine, nil)
	if !errors.Is(err, jobs.ErrYielded) || !result.RunAfter.IsZero() {
		t.Fatalf("first claim = %+v, %v; want an immediate yield", result, err)
	}
	if len(st.records) != enrichmentBatchSize {
		t.Fatalf("first claim checked %d, want %d", len(st.records), enrichmentBatchSize)
	}
	if _, err := runEnrichment(t, st, engine, result.Checkpoint); err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(st.records) != total {
		t.Errorf("checked %d after resume, want %d", len(st.records), total)
	}
}

func TestEntityEnrichmentValidate(t *testing.T) {
	if err := testEnrichment().Validate(); err != nil {
		t.Errorf("complete enrichment: %v", err)
	}
	broken := testEnrichment()
	broken.Names = nil
	if err := broken.Validate(); err == nil {
		t.Errorf("enrichment without Names validated")
	}
}
