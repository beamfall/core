package compat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

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
	// Media item.
	MemberRelations map[string]bool
	// ParentRelations are the relations by which a mapped container climbs to
	// its parent mapped container.
	ParentRelations map[string]bool
	// ProviderSchemes maps a scraper ID to the provider scheme of the
	// candidate IDs it stores, for scrapers whose lookup is a by-ID lookup.
	ProviderSchemes map[string]string
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
	id, name, itemType, sortKey string
	year                        int
	index                       *int
	width, height               int
	leaf                        bool
	order                       int64
	parent, top                 *catalogNode
	children                    []*catalogNode
	providers                   map[string]string
}

type catalogIndex struct {
	roots  []*catalogNode
	byID   map[string]*catalogNode
	leaves []*catalogNode
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

// buildIndex reads one Library's visible Media and arranges the mapped ones
// under their mapped containers. Visibility is decided by the store before
// anything here counts or pages.
func (h *Handler) buildIndex(ctx context.Context, filter store.LibraryFilter, lib catalogLibrary) (*catalogIndex, error) {
	index := &catalogIndex{byID: map[string]*catalogNode{}}
	media, err := h.store.LibraryVisibleMedia(ctx, filter, lib.library.ID)
	if err != nil || len(media) == 0 {
		return index, err
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
			id: string(link.EntityID), name: link.Title, itemType: interop.KindTypes[link.Kind],
			sortKey: link.Title, providers: map[string]string{},
		}
		containers[link.EntityID] = node
		return node
	}
	mappedLink := func(link store.TitledEntityLink, relations map[string]bool) bool {
		return link.LibraryID == lib.library.ID && relations[link.Relation] && interop.KindTypes[link.Kind] != ""
	}
	var frontier []store.EntityID
	for _, id := range mapped {
		leaf := index.byID[string(id)]
		for _, link := range holders[id] {
			if !mappedLink(link, interop.MemberRelations) {
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
				if !mappedLink(link, interop.ParentRelations) {
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
// target under n would make a cycle.
func (n *catalogNode) climbsTo(target *catalogNode) bool {
	for node, depth := n, 0; node != nil && depth <= catalogParentDepth+1; node, depth = node.parent, depth+1 {
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
			if key := providerKeys[strings.ToLower(externalID.Scheme)]; key != "" && externalID.Value != "" {
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
				if ok && key != "" && record.CandidateID != "" && record.PluginID == *lib.library.PluginID {
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
	if node.top != node {
		item.SeriesID = node.top.id
		if node.parent != nil && node.parent != node.top {
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

// findNode searches the eligible Libraries for one visible item.
func (h *Handler) findNode(ctx context.Context, filter store.LibraryFilter, libraries []catalogLibrary, id string) (*catalogNode, error) {
	for _, lib := range libraries {
		index, err := h.buildIndex(ctx, filter, lib)
		if err != nil {
			return nil, err
		}
		if node := index.byID[id]; node != nil {
			return node, nil
		}
	}
	return nil, nil
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
	found := map[string]*catalogNode{}
	for _, lib := range libraries {
		index, err := h.buildIndex(r.Context(), filter, lib)
		if err != nil {
			http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
			return
		}
		for id := range wanted {
			if node := index.byID[id]; node != nil && found[id] == nil {
				found[id] = node
			}
		}
	}
	items := []compatItem{}
	for _, id := range order {
		if node := found[id]; node != nil {
			items = append(items, h.itemJSON(node))
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
	index, err := h.buildIndex(r.Context(), filter, lib)
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
	start := queryInt(query, "StartIndex", 0)
	limit := catalogUnboundedLimit
	if raw := queryValue(query, "Limit"); raw != "" {
		limit = min(max(queryInt(query, "Limit", 0), 0), catalogPageLimit)
	}
	page := roots[min(start, len(roots)):min(start+limit, len(roots))]
	writeJSON(w, itemList{Items: h.itemsJSON(page), TotalRecordCount: len(roots), StartIndex: start})
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
	index, err := h.buildIndex(r.Context(), filter, lib)
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
	node, err := h.findNode(r.Context(), filter, libraries, id)
	if err != nil {
		http.Error(w, "compatibility catalog unavailable", http.StatusInternalServerError)
		return
	}
	if node == nil || node.leaf || node.parent != nil {
		http.NotFound(w, r)
		return
	}
	var nodes []*catalogNode
	if !leaves {
		for _, child := range node.children {
			if !child.leaf {
				nodes = append(nodes, child)
			}
		}
	} else {
		holder := queryValue(r.URL.Query(), "seasonId")
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
	items := h.itemsJSON(nodes)
	writeJSON(w, itemList{Items: items, TotalRecordCount: len(items)})
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
		users[i] = compatUserSummary{ID: string(profile.ID), Name: profile.Name, ServerID: h.serverID}
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
	if setupID, setupName := h.setupUser(); id == setupID {
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
	detail.compatUserSummary = compatUserSummary{ID: string(profile.ID), Name: profile.Name, ServerID: h.serverID}
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

func (h *Handler) optedInProfile(ctx context.Context, id string) (*store.Profile, error) {
	profiles, err := h.optedInProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for i := range profiles {
		if string(profiles[i].ID) == id {
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
// vary the case.
func queryValue(query map[string][]string, name string) string {
	for key, values := range query {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
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
