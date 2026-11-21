package compat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

// Interop is one plugin's catalog presentation to compatibility tools,
// flattened by the app from the plugin bundle. Every token is plugin data:
// core compares them for equality and never names one (invariant #1).
type Interop struct {
	// MediaField names the Media text field whose value selects an item type.
	MediaField string
	// MediaTypes maps a MediaField value to an item type.
	MediaTypes map[string]string
	// KindTypes maps a container Entity kind to an item type.
	KindTypes map[string]string
	// ScopeTypes maps a Library scope to a collection type.
	ScopeTypes map[string]string
	// MemberRelations are the relations by which a mapped container holds one
	// Media item, keyed by MemberRelationKey.
	MemberRelations map[string]bool
	// ParentRelations are the relations by which a mapped container climbs to
	// its parent mapped container, keyed by ParentRelationKey.
	ParentRelations map[string]bool
	// ProviderSchemes maps a scraper ID to the provider scheme of the
	// candidate IDs it stores, for scrapers whose lookup is a by-ID lookup.
	ProviderSchemes map[string]string
}

// MemberRelationKey keys a member relation by the container kind declaring it.
func MemberRelationKey(holderKind, relation string) string {
	return holderKind + "\x00" + relation
}

// ParentRelationKey keys a parent relation by the kind declaring it and the
// kind it climbs to.
func ParentRelationKey(childKind, relation, parentKind string) string {
	return childKind + "\x00" + relation + "\x00" + parentKind
}

const (
	// catalogSequenceField and catalogIndexField are the generic ordinal Media
	// fields: a container's ordinal is the smallest sequence of its members,
	// a Media item's ordinal its own index.
	catalogSequenceField = "sequence"
	catalogIndexField    = "index"

	catalogPageLimit      = 1000
	catalogUnboundedLimit = 50000
	catalogLatestDefault  = 20
	catalogLatestLimit    = 100
	catalogParentDepth    = 8
	catalogProviderIDMax  = 64

	// catalogIndexTTL is how long a built index serves repeat reads for the
	// same Library and visibility filter. The tools read one item per request
	// across a whole Library, so rebuilding each time is quadratic per sync.
	// Library eligibility, restriction and sealing and each served item's
	// visibility, title and card fields are still read on every request; see
	// libraryIndex and visibleNodes.
	catalogIndexTTL     = 15 * time.Second
	catalogIndexEntries = 32
	catalogIndexBuilds  = 2
)

// providerKeys are the ProviderIds keys the tools read, by lowercase scheme.
var providerKeys = map[string]string{"tmdb": "Tmdb", "imdb": "Imdb", "tvdb": "Tvdb", "anidb": "AniDB"}

type compatItem struct {
	ID             string              `json:"Id"`
	Name           string              `json:"Name"`
	ServerID       string              `json:"ServerId"`
	Type           string              `json:"Type"`
	ProviderIDs    map[string]string   `json:"ProviderIds"`
	IndexNumber    *int                `json:"IndexNumber,omitempty"`
	ProductionYear int                 `json:"ProductionYear,omitempty"`
	SeriesID       string              `json:"SeriesId,omitempty"`
	SeasonID       string              `json:"SeasonId,omitempty"`
	MediaSources   []compatMediaSource `json:"MediaSources,omitempty"`
}

type compatMediaSource struct {
	MediaStreams []compatMediaStream `json:"MediaStreams"`
}

type compatMediaStream struct {
	Type   string `json:"Type"`
	Width  int    `json:"Width"`
	Height int    `json:"Height"`
}

type itemList struct {
	Items            []compatItem `json:"Items"`
	TotalRecordCount int          `json:"TotalRecordCount"`
	StartIndex       int          `json:"StartIndex"`
}

type mediaFolder struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	Type           string `json:"Type"`
	CollectionType string `json:"CollectionType"`
}

type mediaFolderList struct {
	Items            []mediaFolder `json:"Items"`
	TotalRecordCount int           `json:"TotalRecordCount"`
	StartIndex       int           `json:"StartIndex"`
}

// catalogLibrary is one eligible Library with its plugin's interop.
type catalogLibrary struct {
	library    store.Library
	interop    Interop
	collection string
}

// catalogNode is one item of a Library's compatibility tree: a visible Media
// item (a leaf) or a mapped container that holds one.
type catalogNode struct {
	id, kind, name, itemType string
	sortKey                  string
	year                     int
	index                    *int
	width, height            int
	leaf                     bool
	order                    int64
	parent, top              *catalogNode
	children                 []*catalogNode
	providers                map[string]string
}

type catalogIndex struct {
	roots  []*catalogNode
	byID   map[string]*catalogNode
	leaves []*catalogNode

	// absent holds IDs of this Library a fresh read confirmed are not in the
	// index, so a repeat lookup of one does not read the Library again.
	mu     sync.Mutex
	absent map[string]bool
}

func (x *catalogIndex) isAbsent(id string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.absent[id]
}

func (x *catalogIndex) markAbsent(ids []string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, id := range ids {
		if x.byID[id] == nil {
			if x.absent == nil {
				x.absent = map[string]bool{}
			}
			x.absent[id] = true
		}
	}
}

// catalogLibraries returns the eligible Libraries the scope sees without
// Reveal whose plugin maps their scope to a collection type. Encrypted and
// sealed Libraries are never listed.
func (h *Handler) catalogLibraries(ctx context.Context, scope auth.Scope) ([]catalogLibrary, store.LibraryFilter, error) {
	filter := scope.LibraryFilter()
	filter.Revealed = false
	if h.store == nil || h.eligible == nil {
		return nil, filter, nil
	}
	eligible, err := h.eligible(ctx)
	if err != nil {
		return nil, filter, err
	}
	libraries, err := h.store.Libraries(ctx, filter)
	if err != nil {
		return nil, filter, err
	}
	var out []catalogLibrary
	for _, library := range libraries {
		if !eligible[library.ID] || library.IsRestricted || library.Encrypted || library.PluginID == nil {
			continue
		}
		if h.sealed != nil && h.sealed(library.ID) {
			continue
		}
		interop, ok := h.interop[*library.PluginID]
		if !ok {
			continue
		}
		collection := interop.ScopeTypes[library.FamilyScope]
		if collection == "" {
			continue
		}
		out = append(out, catalogLibrary{library: library, interop: interop, collection: collection})
	}
	return out, filter, nil
}

// indexCache holds recently built indexes by Library and visibility filter.
// An index is never changed after it is built, so readers share it.
type indexCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]cachedIndex
	slots   chan struct{}
}

type cachedIndex struct {
	built   time.Time // when the read the index was built from began
	visible [sha256.Size]byte
	index   *catalogIndex
}

// libraryIndex returns the Library's index, reusing one built for the same
// filter within catalogIndexTTL while the store still reports the same visible
// items. The visible set is read on every request, so certification, trash and
// Reveal changes apply at once; only container links, ordinals and provider
// IDs of unchanged items can lag. At most catalogIndexBuilds reads or builds
// run at once, and a request that waited finds the index the one ahead of it
// built.
func (h *Handler) libraryIndex(ctx context.Context, filter store.LibraryFilter, lib catalogLibrary) (*catalogIndex, error) {
	return h.readIndex(ctx, filter, lib, false)
}

// freshIndex returns an index of the Library read after this call began: one
// another request read meanwhile, or a new read. A changed container link or
// provider ID then shows even when the visible items are unchanged.
func (h *Handler) freshIndex(ctx context.Context, filter store.LibraryFilter, lib catalogLibrary) (*catalogIndex, error) {
	return h.readIndex(ctx, filter, lib, true)
}

func (h *Handler) readIndex(ctx context.Context, filter store.LibraryFilter, lib catalogLibrary, fresh bool) (*catalogIndex, error) {
	key, err := indexKey(filter, lib)
	if err != nil {
		return nil, err
	}
	c := &h.indexes
	asked := c.clock()
	release, err := c.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if fresh {
		if index := c.readSince(key, asked); index != nil {
			return index, nil
		}
	}
	read := c.clock()
	media, err := h.store.LibraryVisibleMedia(ctx, filter, lib.library.ID)
	if err != nil {
		return nil, err
	}
	visible := visibleFingerprint(media)
	if !fresh {
		if index := c.lookup(key, visible); index != nil {
			return index, nil
		}
	}
	index, err := h.buildIndex(ctx, media, lib)
	if err != nil {
		return nil, err
	}
	c.store(key, visible, index, read)
	return index, nil
}

// recentIndex returns the Library's index as built within catalogIndexTTL,
// without reading the visible set again, or else libraryIndex's. A caller
// confirms whatever it serves from it with visibleNodes.
func (h *Handler) recentIndex(ctx context.Context, filter store.LibraryFilter, lib catalogLibrary) (*catalogIndex, error) {
	key, err := indexKey(filter, lib)
	if err != nil {
		return nil, err
	}
	if index := h.indexes.recent(key); index != nil {
		return index, nil
	}
	return h.libraryIndex(ctx, filter, lib)
}

func indexKey(filter store.LibraryFilter, lib catalogLibrary) (string, error) {
	fingerprint, err := json.Marshal(filter)
	if err != nil {
		return "", err
	}
	return string(lib.library.ID) + "\x00" + string(fingerprint), nil
}

// visibleNodes returns current copies of the nodes that still have a visible
// item: a leaf the store still reports visible, or a container that is still
// an untrashed Entity of the Library and holds one. A node under a container
// that is no longer such an Entity is dropped until the index is rebuilt. Each
// copy carries the item's current title, year and dimensions; a container's
// year is the earliest of its visible leaves'. It reads only
// the leaves under the given nodes and the containers on and above them.
func (h *Handler) visibleNodes(ctx context.Context, filter store.LibraryFilter, lib catalogLibrary, nodes []*catalogNode) ([]*catalogNode, error) {
	var ids []store.MediaID
	var entities []store.EntityID
	seen := map[*catalogNode]bool{}
	entity := func(n *catalogNode) {
		if !seen[n] {
			seen[n] = true
			entities = append(entities, store.EntityID(n.id))
		}
	}
	walked := map[*catalogNode]bool{}
	var collect func(*catalogNode)
	collect = func(n *catalogNode) {
		if walked[n] {
			return
		}
		walked[n] = true
		if n.leaf {
			ids = append(ids, store.MediaID(n.id))
			return
		}
		entity(n)
		for _, child := range n.children {
			collect(child)
		}
	}
	for _, n := range nodes {
		collect(n)
		for p := n.parent; p != nil; p = p.parent {
			entity(p)
		}
	}
	media, err := h.store.LibraryVisibleMediaItems(ctx, filter, lib.library.ID, ids)
	if err != nil {
		return nil, err
	}
	titles, err := h.store.LibraryLiveEntityTitles(ctx, lib.library.ID, entities)
	if err != nil {
		return nil, err
	}
	live := func(n *catalogNode) bool {
		_, ok := titles[store.EntityID(n.id)]
		return ok
	}
	var holdsVisible func(*catalogNode) bool
	holdsVisible = func(n *catalogNode) bool {
		if n.leaf {
			_, ok := media[store.MediaID(n.id)]
			return ok
		}
		return live(n) && slices.ContainsFunc(n.children, holdsVisible)
	}
	var kept []*catalogNode
	for _, n := range nodes {
		if !holdsVisible(n) {
			continue
		}
		held := true
		for p := n.parent; p != nil && held; p = p.parent {
			held = live(p)
		}
		if !held {
			continue
		}
		current := *n
		if n.leaf {
			m := media[store.MediaID(n.id)]
			current.name, current.sortKey, current.year = m.Title, m.TitleKey(), m.Year
			current.width, current.height = m.Width, m.Height
		} else {
			current.name = titles[store.EntityID(n.id)]
			current.sortKey = current.name
			current.year = earliestYear(n, media)
		}
		kept = append(kept, &current)
	}
	return kept, nil
}

// earliestYear returns the earliest year among the visible leaves under n, the
// year a container carries.
func earliestYear(n *catalogNode, media map[store.MediaID]store.VisibleMedia) int {
	if n.leaf {
		return media[store.MediaID(n.id)].Year
	}
	year := 0
	for _, child := range n.children {
		if y := earliestYear(child, media); y > 0 && (year == 0 || y < year) {
			year = y
		}
	}
	return year
}

func (c *indexCache) acquire(ctx context.Context) (func(), error) {
	c.mu.Lock()
	if c.slots == nil {
		c.slots = make(chan struct{}, catalogIndexBuilds)
	}
	slots := c.slots
	c.mu.Unlock()
	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *indexCache) clock() time.Time {
	if c.now == nil {
		c.now = time.Now
	}
	return c.now()
}

func (c *indexCache) lookup(key string, visible [sha256.Size]byte) *catalogIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || entry.visible != visible || c.clock().Sub(entry.built) >= catalogIndexTTL {
		return nil
	}
	return entry.index
}

// recent returns the index stored under key within catalogIndexTTL, whatever
// visible set it was built from.
func (c *indexCache) recent(key string) *catalogIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || c.clock().Sub(entry.built) >= catalogIndexTTL {
		return nil
	}
	return entry.index
}

// readSince returns the index stored under key whose read began after since.
func (c *indexCache) readSince(key string, since time.Time) *catalogIndex {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !entry.built.After(since) {
		return nil
	}
	return entry.index
}

// store keeps index, built from a read that began at read, under key,
// evicting the oldest entry when full.
func (c *indexCache) store(key string, visible [sha256.Size]byte, index *catalogIndex, read time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedIndex{}
	}
	if _, ok := c.entries[key]; !ok && len(c.entries) >= catalogIndexEntries {
		oldest := ""
		for k, entry := range c.entries {
			if oldest == "" || entry.built.Before(c.entries[oldest].built) {
				oldest = k
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = cachedIndex{built: read, visible: visible, index: index}
}

// visibleFingerprint digests every field of the visible items that an index
// carries, so any visibility or card change forces a rebuild.
func visibleFingerprint(media []store.VisibleMedia) [sha256.Size]byte {
	digest := sha256.New()
	for _, m := range media {
		_, _ = fmt.Fprintf(digest, "%q %q %q %d %d %d %d\n", m.ID, m.Title, m.SortTitle, m.Year, m.Width, m.Height, m.Order)
	}
	var sum [sha256.Size]byte
	digest.Sum(sum[:0])
	return sum
}

// buildIndex reads one Library's visible Media and arranges the mapped ones
// under their mapped containers. Visibility is decided by the store before
// anything here counts or pages.
func (h *Handler) buildIndex(ctx context.Context, media []store.VisibleMedia, lib catalogLibrary) (*catalogIndex, error) {
	index := &catalogIndex{byID: map[string]*catalogNode{}}
	if len(media) == 0 {
		return index, nil
	}
	interop := lib.interop
	ids := make([]store.MediaID, len(media))
	for i, m := range media {
		ids[i] = m.ID
	}
	fields, err := h.store.MediaCardFieldValues(ctx, ids, []string{interop.MediaField, catalogSequenceField, catalogIndexField})
	if err != nil {
		return nil, err
	}
	var mapped []store.MediaID
	sequence := map[*catalogNode]int{}
	for _, m := range media {
		values := fields[m.ID]
		itemType := interop.MediaTypes[values[interop.MediaField].Text]
		if itemType == "" {
			continue
		}
		node := &catalogNode{
			id: string(m.ID), name: m.Title, itemType: itemType, sortKey: m.TitleKey(),
			year: m.Year, width: m.Width, height: m.Height, leaf: true, order: m.Order,
			providers: map[string]string{},
		}
		if field, ok := values[catalogIndexField]; ok {
			node.index = ordinal(field.Number)
		}
		if field, ok := values[catalogSequenceField]; ok {
			sequence[node] = int(field.Number)
		}
		index.byID[node.id] = node
		index.leaves = append(index.leaves, node)
		mapped = append(mapped, m.ID)
	}
	if err := h.attachContainers(ctx, lib, index, mapped, sequence); err != nil {
		return nil, err
	}
	if err := h.attachProviderIDs(ctx, lib, index, mapped); err != nil {
		return nil, err
	}
	return index, nil
}

// attachContainers links each leaf to the mapped container holding it, climbs
// to each container's mapped parent, and sorts the resulting tree.
func (h *Handler) attachContainers(ctx context.Context, lib catalogLibrary, index *catalogIndex, mapped []store.MediaID, sequence map[*catalogNode]int) error {
	interop := lib.interop
	holders, err := h.store.MediaHolderLinksBatch(ctx, mapped)
	if err != nil {
		return err
	}
	containers := map[store.EntityID]*catalogNode{}
	container := func(link store.TitledEntityLink) *catalogNode {
		if node, ok := containers[link.EntityID]; ok {
			return node
		}
		node := &catalogNode{
			id: string(link.EntityID), kind: link.Kind, name: link.Title, itemType: interop.KindTypes[link.Kind],
			sortKey: link.Title, providers: map[string]string{},
		}
		containers[link.EntityID] = node
		return node
	}
	mappedLink := func(link store.TitledEntityLink) bool {
		return link.LibraryID == lib.library.ID && interop.KindTypes[link.Kind] != ""
	}
	var frontier []store.EntityID
	for _, id := range mapped {
		leaf := index.byID[string(id)]
		for _, link := range holders[id] {
			if !mappedLink(link) || !interop.MemberRelations[MemberRelationKey(link.Kind, link.Relation)] {
				continue
			}
			_, seen := containers[link.EntityID]
			holder := container(link)
			leaf.parent = holder
			if !seen {
				frontier = append(frontier, link.EntityID)
			}
			if value, ok := sequence[leaf]; ok && (holder.index == nil || value < *holder.index) {
				holder.index = ordinal(float64(value))
			}
			break
		}
	}
	for depth := 0; depth < catalogParentDepth && len(frontier) > 0; depth++ {
		targets, err := h.store.EntityTargetLinksBatch(ctx, frontier)
		if err != nil {
			return err
		}
		var next []store.EntityID
		for _, id := range frontier {
			child := containers[id]
			for _, link := range targets[id] {
				if !mappedLink(link) || !interop.ParentRelations[ParentRelationKey(child.kind, link.Relation, link.Kind)] {
					continue
				}
				_, seen := containers[link.EntityID]
				parent := container(link)
				if parent.climbsTo(child) {
					break
				}
				child.parent = parent
				if !seen {
					next = append(next, link.EntityID)
				}
				break
			}
		}
		frontier = next
	}
	// A container's year is its earliest member year.
	for _, leaf := range index.leaves {
		for node := leaf.parent; node != nil; node = node.parent {
			if leaf.year > 0 && (node.year == 0 || leaf.year < node.year) {
				node.year = leaf.year
			}
		}
	}
	for _, node := range containers {
		index.byID[node.id] = node
	}
	for _, node := range index.byID {
		node.top = node
		for node.top.parent != nil {
			node.top = node.top.parent
		}
		if node.parent == nil {
			index.roots = append(index.roots, node)
		} else {
			node.parent.children = append(node.parent.children, node)
		}
	}
	slices.SortFunc(index.roots, compareTitle)
	for _, node := range index.byID {
		slices.SortFunc(node.children, compareOrdinal)
	}
	return nil
}

// climbsTo reports whether target is n or one of n's ancestors, so linking
// target under n would make a cycle. It walks the whole chain: a link is only
// made when it closes no cycle, so every chain ends.
func (n *catalogNode) climbsTo(target *catalogNode) bool {
	for node := n; node != nil; node = node.parent {
		if node == target {
			return true
		}
	}
	return false
}

// attachProviderIDs gives each leaf its stored external IDs and each root the
// by-ID scraper candidate of its first identified member in tree order.
func (h *Handler) attachProviderIDs(ctx context.Context, lib catalogLibrary, index *catalogIndex, mapped []store.MediaID) error {
	external, err := h.store.MediaExternalIDsBatch(ctx, mapped)
	if err != nil {
		return err
	}
	provenance, err := h.store.MediaMetadataProvenanceBatch(ctx, mapped)
	if err != nil {
		return err
	}
	for _, id := range mapped {
		leaf := index.byID[string(id)]
		for _, externalID := range external[id] {
			if key := providerKeys[strings.ToLower(externalID.Scheme)]; key != "" && validProviderID(key, externalID.Value) {
				leaf.providers[key] = externalID.Value
			}
		}
	}
	var walk func(nodes []*catalogNode)
	walk = func(nodes []*catalogNode) {
		for _, node := range nodes {
			if node.leaf {
				record, ok := provenance[store.MediaID(node.id)]
				key := providerKeys[lib.interop.ProviderSchemes[record.ScraperID]]
				if ok && key != "" && validProviderID(key, record.CandidateID) && record.PluginID == *lib.library.PluginID {
					if _, set := node.top.providers[key]; !set {
						node.top.providers[key] = record.CandidateID
					}
				}
			}
			walk(node.children)
		}
	}
	walk(index.roots)
	return nil
}

// validProviderID reports whether value has the form of an identifier of the
// provider the key names: digits, or tt and digits for Imdb. A stored value
// carrying a path, file name, URL or other text is never echoed.
func validProviderID(key, value string) bool {
	if len(value) > catalogProviderIDMax {
		return false
	}
	if key == "Imdb" {
		var ok bool
		if value, ok = strings.CutPrefix(value, "tt"); !ok {
			return false
		}
	}
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func ordinal(value float64) *int {
	n := int(value)
	return &n
}

func compareTitle(a, b *catalogNode) int {
	if c := strings.Compare(strings.ToLower(a.sortKey), strings.ToLower(b.sortKey)); c != 0 {
		return c
	}
	return strings.Compare(a.id, b.id)
}

func compareOrdinal(a, b *catalogNode) int {
	switch {
	case a.index != nil && b.index == nil:
		return -1
	case a.index == nil && b.index != nil:
		return 1
	case a.index != nil && *a.index != *b.index:
		if *a.index < *b.index {
			return -1
		}
		return 1
	}
	return compareTitle(a, b)
}

func (h *Handler) itemJSON(node *catalogNode) compatItem {
	item := compatItem{
		ID: node.id, Name: node.name, ServerID: h.serverID, Type: node.itemType,
		ProviderIDs: node.providers, IndexNumber: node.index, ProductionYear: node.year,
	}
	if item.ProviderIDs == nil {
		item.ProviderIDs = map[string]string{}
	}
	if node.parent != nil {
		item.SeriesID = node.top.id
		if node.parent != node.top {
			item.SeasonID = node.parent.id
		}
	}
	if node.leaf {
		item.MediaSources = []compatMediaSource{{MediaStreams: []compatMediaStream{{Type: "Video", Width: node.width, Height: node.height}}}}
	}
	return item
}

func (h *Handler) itemsJSON(nodes []*catalogNode) []compatItem {
	items := make([]compatItem, len(nodes))
	for i, node := range nodes {
		items[i] = h.itemJSON(node)
	}
	return items
}

// catalogScope authenticates a read and resolves the eligible Libraries. It
// writes the response on failure.
func (h *Handler) catalogScope(w http.ResponseWriter, r *http.Request) ([]catalogLibrary, store.LibraryFilter, bool) {
	scope, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead)
	if !ok {
		return nil, store.LibraryFilter{}, false
	}
	libraries, filter, err := h.catalogLibraries(r.Context(), scope)
	if err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return nil, filter, false
	}
	return libraries, filter, true
}

// catalogHit is one visible item and the Library it was found in.
type catalogHit struct {
	node *catalogNode
	lib  catalogLibrary
}

// findNodes finds the indexed items with the given IDs, without confirming
// they are still visible. It reads only the eligible Libraries that hold one,
// each from its recent index. An ID the recent index lacks reads the Library
// afresh, once per index: one still missing from that fresh index is
// remembered on it as absent.
func (h *Handler) findNodes(ctx context.Context, filter store.LibraryFilter, libraries []catalogLibrary, ids []string) (map[string]catalogHit, error) {
	found := map[string]catalogHit{}
	if len(libraries) == 0 || len(ids) == 0 {
		return found, nil
	}
	owners, err := h.store.CatalogItemLibraries(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, lib := range libraries {
		var wanted []string
		for _, id := range ids {
			if owners[id] == lib.library.ID {
				wanted = append(wanted, id)
			}
		}
		if len(wanted) == 0 {
			continue
		}
		index, err := h.recentIndex(ctx, filter, lib)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(wanted, func(id string) bool { return index.byID[id] == nil && !index.isAbsent(id) }) {
			if index, err = h.freshIndex(ctx, filter, lib); err != nil {
				return nil, err
			}
			index.markAbsent(wanted)
		}
		for _, id := range wanted {
			if node := index.byID[id]; node != nil {
				found[id] = catalogHit{node: node, lib: lib}
			}
		}
	}
	return found, nil
}

// lookupNodes finds the visible items with the given IDs, as current copies,
// so a per-item sync reads each item's rows rather than its whole Library.
func (h *Handler) lookupNodes(ctx context.Context, filter store.LibraryFilter, libraries []catalogLibrary, ids []string) (map[string]catalogHit, error) {
	hits, err := h.findNodes(ctx, filter, libraries, ids)
	if err != nil {
		return nil, err
	}
	found := map[string]catalogHit{}
	for _, lib := range libraries {
		var nodes []*catalogNode
		for _, id := range ids {
			if hit, ok := hits[id]; ok && hit.lib.library.ID == lib.library.ID {
				nodes = append(nodes, hit.node)
			}
		}
		if len(nodes) == 0 {
			continue
		}
		if nodes, err = h.visibleNodes(ctx, filter, lib, nodes); err != nil {
			return nil, err
		}
		for _, node := range nodes {
			found[node.id] = catalogHit{node: node, lib: lib}
		}
	}
	return found, nil
}

func findLibrary(libraries []catalogLibrary, id string) (catalogLibrary, bool) {
	for _, lib := range libraries {
		if string(lib.library.ID) == id {
			return lib, true
		}
	}
	return catalogLibrary{}, false
}

// mediaFolders lists the eligible Libraries as collection folders, with no
// paths.
func (h *Handler) mediaFolders(w http.ResponseWriter, r *http.Request, _ net.IP) {
	libraries, _, ok := h.catalogScope(w, r)
	if !ok {
		return
	}
	folders := make([]mediaFolder, len(libraries))
	for i, lib := range libraries {
		folders[i] = mediaFolder{
			ID: string(lib.library.ID), Name: lib.library.Title,
			Type: "CollectionFolder", CollectionType: lib.collection,
		}
	}
	writeJSON(w, mediaFolderList{Items: folders, TotalRecordCount: len(folders)})
}

// items serves Items?ParentId= and Items?ids=. Without either it lists
// nothing, so a path lookup learns nothing (ADR-0171 §2.8).
func (h *Handler) items(w http.ResponseWriter, r *http.Request, _ net.IP) {
	libraries, filter, ok := h.catalogScope(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	parentID, ids := queryValue(query, "ParentId"), queryValue(query, "ids")
	switch {
	case ids != "":
		h.itemsByID(w, r, filter, libraries, ids)
	case parentID != "":
		h.libraryItems(w, r, filter, libraries, parentID)
	default:
		writeJSON(w, itemList{Items: []compatItem{}})
	}
}

func (h *Handler) itemsByID(w http.ResponseWriter, r *http.Request, filter store.LibraryFilter, libraries []catalogLibrary, ids string) {
	wanted := map[string]bool{}
	var order []string
	for _, id := range strings.Split(ids, ",") {
		if id = strings.TrimSpace(id); id != "" && !wanted[id] && len(order) < catalogPageLimit {
			wanted[id] = true
			order = append(order, id)
		}
	}
	found, err := h.lookupNodes(r.Context(), filter, libraries, order)
	if err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return
	}
	items := []compatItem{}
	for _, id := range order {
		if hit, ok := found[id]; ok {
			items = append(items, h.itemJSON(hit.node))
		}
	}
	writeJSON(w, itemList{Items: items, TotalRecordCount: len(items)})
}

func (h *Handler) libraryItems(w http.ResponseWriter, r *http.Request, filter store.LibraryFilter, libraries []catalogLibrary, parentID string) {
	lib, ok := findLibrary(libraries, parentID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	index, err := h.libraryIndex(r.Context(), filter, lib)
	if err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return
	}
	query := r.URL.Query()
	roots := index.roots
	if types := queryValue(query, "IncludeItemTypes"); types != "" {
		include := map[string]bool{}
		for _, itemType := range strings.Split(types, ",") {
			include[strings.ToLower(strings.TrimSpace(itemType))] = true
		}
		roots = slices.DeleteFunc(slices.Clone(roots), func(node *catalogNode) bool {
			return !include[strings.ToLower(node.itemType)]
		})
	}
	h.writePage(w, query, roots)
}

// writePage writes the StartIndex/Limit window of nodes with the full count.
// Without a Limit the window is bounded by catalogUnboundedLimit.
func (h *Handler) writePage(w http.ResponseWriter, query map[string][]string, nodes []*catalogNode) {
	start := min(queryInt(query, "StartIndex", 0), len(nodes))
	limit := catalogUnboundedLimit
	if queryValue(query, "Limit") != "" {
		limit = min(queryInt(query, "Limit", 0), catalogPageLimit)
	}
	page := nodes[start : start+min(limit, len(nodes)-start)]
	writeJSON(w, itemList{Items: h.itemsJSON(page), TotalRecordCount: len(nodes), StartIndex: start})
}

// latestItems serves Items/Latest?ParentId=: the newest visible Media items
// of one eligible Library, as a bare array.
func (h *Handler) latestItems(w http.ResponseWriter, r *http.Request, _ net.IP) {
	libraries, filter, ok := h.catalogScope(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	lib, ok := findLibrary(libraries, queryValue(query, "ParentId"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	index, err := h.libraryIndex(r.Context(), filter, lib)
	if err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return
	}
	limit := min(max(queryInt(query, "Limit", catalogLatestDefault), 0), catalogLatestLimit)
	leaves := slices.Clone(index.leaves)
	slices.SortFunc(leaves, func(a, b *catalogNode) int {
		if a.order != b.order {
			if a.order > b.order {
				return -1
			}
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
	writeJSON(w, h.itemsJSON(leaves[:min(limit, len(leaves))]))
}

// containerChildren serves Shows/{id}/Seasons and Shows/{id}/Episodes: the
// ordered children of a visible top container, or for Episodes every leaf
// under it, optionally narrowed to one direct holder by seasonId.
func (h *Handler) containerChildren(w http.ResponseWriter, r *http.Request, id string, leaves bool) {
	libraries, filter, ok := h.catalogScope(w, r)
	if !ok {
		return
	}
	found, err := h.findNodes(r.Context(), filter, libraries, []string{id})
	if err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return
	}
	hit, ok := found[id]
	node := hit.node
	if !ok || node.leaf || node.parent != nil {
		http.NotFound(w, r)
		return
	}
	var nodes []*catalogNode
	holder := ""
	if !leaves {
		for _, child := range node.children {
			if !child.leaf {
				nodes = append(nodes, child)
			}
		}
	} else {
		holder = queryValue(r.URL.Query(), "seasonId")
		if holder != "" && !holdsContainer(node, holder) {
			http.NotFound(w, r)
			return
		}
		var walk func(*catalogNode)
		walk = func(n *catalogNode) {
			for _, child := range n.children {
				if child.leaf {
					if holder == "" || (child.parent != nil && child.parent.id == holder) {
						nodes = append(nodes, child)
					}
					continue
				}
				walk(child)
			}
		}
		walk(node)
	}
	if nodes, err = h.visibleNodes(r.Context(), filter, hit.lib, nodes); err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return
	}
	// With nothing to list, the container itself may be gone: a middle container left
	// with no visible member is hidden, and a top container is listed empty only
	// while it still holds a visible item.
	if len(nodes) == 0 {
		shown := false
		if holder == "" {
			kept, err := h.visibleNodes(r.Context(), filter, hit.lib, []*catalogNode{node})
			if err != nil {
				http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
				return
			}
			shown = len(kept) > 0
		}
		if !shown {
			http.NotFound(w, r)
			return
		}
	}
	h.writePage(w, r.URL.Query(), nodes)
}

// holdsContainer reports whether a container with the given ID sits under node.
func holdsContainer(node *catalogNode, id string) bool {
	for _, child := range node.children {
		if !child.leaf && (child.id == id || holdsContainer(child, id)) {
			return true
		}
	}
	return false
}

// listUsers serves Users: the enabled Profiles opted in to compatibility
// sign-in, each with only its ID and name.
func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request, _ net.IP) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead); !ok {
		return
	}
	profiles, err := h.optedInProfiles(r.Context())
	if err != nil {
		http.Error(w, "compatibility users unavailable", http.StatusInternalServerError)
		return
	}
	users := make([]compatUserSummary, len(profiles))
	for i, profile := range profiles {
		users[i] = compatUserSummary{ID: h.compatUserID(profile.ID), Name: profile.Name, ServerID: h.serverID}
	}
	writeJSON(w, users)
}

type compatUserSummary struct {
	ID       string `json:"Id"`
	Name     string `json:"Name"`
	ServerID string `json:"ServerId"`
}

type compatUserDetail struct {
	compatUserSummary
	Configuration struct {
		GroupedFolders []string `json:"GroupedFolders"`
	} `json:"Configuration"`
}

func (h *Handler) optedInProfiles(ctx context.Context) ([]store.Profile, error) {
	if h.store == nil {
		return nil, nil
	}
	profiles, err := h.store.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(profiles, func(profile store.Profile) bool {
		return !profile.IntegrationSignIn || profile.Disabled || profile.Kind != store.ProfileAdult
	}), nil
}

// user serves Users/{id} for an opted-in Profile or the setup account.
func (h *Handler) user(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead); !ok {
		return
	}
	detail := compatUserDetail{}
	detail.Configuration.GroupedFolders = []string{}
	if setupID, setupName := h.setupUser(); normalizeUserID(id) == setupID {
		detail.compatUserSummary = compatUserSummary{ID: setupID, Name: setupName, ServerID: h.serverID}
		writeJSON(w, detail)
		return
	}
	profile, err := h.optedInProfile(r.Context(), id)
	if err != nil {
		http.Error(w, "compatibility users unavailable", http.StatusInternalServerError)
		return
	}
	if profile == nil {
		http.NotFound(w, r)
		return
	}
	detail.compatUserSummary = compatUserSummary{ID: h.compatUserID(profile.ID), Name: profile.Name, ServerID: h.serverID}
	writeJSON(w, detail)
}

// userImage serves Users/{id}/Images/Primary: the opted-in Profile's stored
// picture, never resized.
func (h *Handler) userImage(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := h.integrationScope(w, r, integrationToken(r.Header), store.IntegrationScopeRead); !ok {
		return
	}
	profile, err := h.optedInProfile(r.Context(), id)
	if err != nil {
		http.Error(w, "compatibility users unavailable", http.StatusInternalServerError)
		return
	}
	if profile == nil {
		http.NotFound(w, r)
		return
	}
	picture, _, err := h.store.ProfilePicture(r.Context(), profile.ID)
	if errors.Is(err, store.ErrNoProfilePicture) || (err == nil && len(picture) == 0) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "compatibility users unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", http.DetectContentType(picture))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(picture)
}

// optedInProfile finds the opted-in Profile with the given compatibility
// account ID. A raw Profile ID is not an account ID and matches nothing.
func (h *Handler) optedInProfile(ctx context.Context, id string) (*store.Profile, error) {
	profiles, err := h.optedInProfiles(ctx)
	if err != nil {
		return nil, err
	}
	id = normalizeUserID(id)
	for i := range profiles {
		if h.compatUserID(profiles[i].ID) == id {
			return &profiles[i], nil
		}
	}
	return nil, nil
}

// serveParameterRoute dispatches the GET routes that carry an ID in the path.
func (h *Handler) serveParameterRoute(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || registeredPath(r.URL.Path) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, Prefix), "/")
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	switch {
	case len(parts) == 2 && parts[0] == "Users":
		h.user(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "Users" && parts[2] == "Images" && parts[3] == "Primary":
		h.userImage(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "Shows" && parts[2] == "Seasons":
		h.containerChildren(w, r, parts[1], false)
	case len(parts) == 3 && parts[0] == "Shows" && parts[2] == "Episodes":
		h.containerChildren(w, r, parts[1], true)
	default:
		return false
	}
	return true
}

// registeredPath reports whether path is an exact route for some method, so
// an ID segment never shadows a route name such as Users/AuthenticateByName.
func registeredPath(path string) bool {
	for route := range jellyfinRoutes {
		if route.path == path {
			return true
		}
	}
	return false
}

// queryValue reads a query parameter by case-insensitive name, as the tools
// vary the case. The exact spelling wins; otherwise the lexically smallest
// matching key does, so a request with several spellings reads one value
// every time.
func queryValue(query map[string][]string, name string) string {
	if values := query[name]; len(values) > 0 {
		return values[0]
	}
	best, found := "", false
	for key, values := range query {
		if strings.EqualFold(key, name) && len(values) > 0 && (!found || key < best) {
			best, found = key, true
		}
	}
	if !found {
		return ""
	}
	return query[best][0]
}

func queryInt(query map[string][]string, name string, fallback int) int {
	raw := queryValue(query, name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
