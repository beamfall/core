package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/plugin"
	scanpkg "github.com/beamfall/core/internal/scan"
	"github.com/beamfall/core/internal/server"
	"github.com/beamfall/core/internal/store"
)

const (
	mediaUploadTargetLimit = 20
	// mediaUploadMemberLimit bounds how many of an Entity's Media are read to
	// find the folder its files live in.
	mediaUploadMemberLimit = 50
)

// mediaUploadPlanner proposes where uploaded files go. It reads each
// upload's name with the library plugin's filename parser, matches an Entity
// by the parsed title, and picks the folder that Entity's files already live
// in, so a later scan finds the file where it lands. Core never names a
// domain: labels come from the plugin bundle.
type mediaUploadPlanner struct {
	st      *store.Store
	parsers map[string]scanpkg.FilenameParser
	plugins *plugin.Registry
	enqueue func(ctx context.Context, job store.Job) error
	now     func() time.Time
}

var _ server.MediaUploadPlanner = (*mediaUploadPlanner)(nil)

func newMediaUploadPlanner(st *store.Store, parsers map[string]scanpkg.FilenameParser, plugins *plugin.Registry, wake *jobWake) *mediaUploadPlanner {
	return &mediaUploadPlanner{
		st:      st,
		parsers: parsers,
		plugins: plugins,
		enqueue: func(ctx context.Context, job store.Job) error {
			_, err := enqueueAndWake(ctx, st, wake, job)
			return err
		},
		now: func() time.Time { return time.Now().UTC() },
	}
}

// mediaUploadLibrary is one Library's plugin and canonical first root.
type mediaUploadLibrary struct {
	filter   store.LibraryFilter
	pluginID string
	parser   scanpkg.FilenameParser
	root     string
	roots    []string
}

func (p *mediaUploadPlanner) library(ctx context.Context, filter store.LibraryFilter, libraryID store.LibraryID) (mediaUploadLibrary, error) {
	libraries, err := p.st.Libraries(ctx, filter)
	if err != nil {
		return mediaUploadLibrary{}, err
	}
	out := mediaUploadLibrary{filter: filter}
	found := false
	for _, library := range libraries {
		if library.ID == libraryID {
			found = true
			if library.PluginID != nil {
				out.pluginID = *library.PluginID
			}
		}
	}
	if !found {
		return mediaUploadLibrary{}, store.ValidationError("library not found")
	}
	out.filter.AllLibraries = false
	out.filter.AllowedIDs = []store.LibraryID{libraryID}
	out.parser = p.parsers[out.pluginID]
	roots, err := p.st.LibraryRoots(ctx, libraryID)
	if err != nil {
		return mediaUploadLibrary{}, err
	}
	for _, root := range roots {
		if canonical, err := filepath.EvalSymlinks(root); err == nil {
			out.roots = append(out.roots, canonical)
		}
	}
	if len(out.roots) == 0 {
		return mediaUploadLibrary{}, store.ValidationError("library has no reachable root")
	}
	out.root = out.roots[0]
	return out, nil
}

func (l mediaUploadLibrary) parse(path string) *server.MediaUploadParse {
	if l.parser == nil {
		return nil
	}
	parsed, ok := l.parser.Parse(path, scanpkg.ParseProbe{})
	if !ok || strings.TrimSpace(parsed.Title) == "" {
		return nil
	}
	return &server.MediaUploadParse{
		Title:    parsed.Title,
		Year:     parsed.Year,
		Sequence: parsed.Sequence,
		Index:    parsed.Index,
		IndexEnd: parsed.IndexEnd,
	}
}

// within reports whether a canonical root holds path.
func (l mediaUploadLibrary) within(path string) bool {
	for _, root := range l.roots {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (l mediaUploadLibrary) isRoot(path string) bool {
	for _, root := range l.roots {
		if path == root {
			return true
		}
	}
	return false
}

func (p *mediaUploadPlanner) ProposeMediaUploads(ctx context.Context, filter store.LibraryFilter, libraryID store.LibraryID, uploads []store.MediaUpload) ([]server.MediaUploadProposal, error) {
	library, err := p.library(ctx, filter, libraryID)
	if err != nil {
		return nil, err
	}
	out := make([]server.MediaUploadProposal, 0, len(uploads))
	for _, upload := range uploads {
		proposal := server.MediaUploadProposal{UploadID: upload.ID, FileName: upload.FileName}
		proposal.Parsed = library.parse(filepath.Join(library.root, filepath.FromSlash(upload.RelativeDir), upload.FileName))
		if proposal.Parsed != nil {
			target, err := p.matchTarget(ctx, library, proposal.Parsed)
			if err != nil {
				return nil, err
			}
			proposal.Target = target
		}
		proposal.Destination = proposeMediaUploadDestination(library.root, upload.RelativeDir, proposal.Parsed, proposal.Target)
		proposal.Rescan = library.parse(filepath.Join(proposal.Destination, upload.FileName))
		if _, err := os.Lstat(filepath.Join(proposal.Destination, upload.FileName)); err == nil {
			proposal.Conflict = true
		}
		out = append(out, proposal)
	}
	return out, nil
}

func proposeMediaUploadDestination(root, relativeDir string, parsed *server.MediaUploadParse, target *server.MediaUploadTarget) string {
	switch {
	case target != nil && target.Destination != "":
		return target.Destination
	case relativeDir != "":
		return filepath.Join(root, filepath.FromSlash(relativeDir))
	case parsed != nil:
		name := mediaUploadFolderName(parsed.Title)
		if parsed.Year > 0 {
			name += " (" + strconv.Itoa(parsed.Year) + ")"
		}
		if name != "" {
			return filepath.Join(root, name)
		}
	}
	return root
}

// mediaUploadFolderName makes a title safe as one path segment.
func mediaUploadFolderName(title string) string {
	title = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return '-'
		}
		return r
	}, strings.TrimSpace(title))
	title = strings.Trim(title, ". ")
	return title
}

// matchTarget picks the Entity whose title matches the parse, preferring an
// equal year, and resolves the folder for the parsed sequence.
func (p *mediaUploadPlanner) matchTarget(ctx context.Context, library mediaUploadLibrary, parsed *server.MediaUploadParse) (*server.MediaUploadTarget, error) {
	items, err := p.st.SearchEntities(ctx, library.filter, parsed.Title, mediaUploadTargetLimit)
	if err != nil {
		return nil, err
	}
	var best *store.SourceItem
	for i := range items {
		item := &items[i]
		if !strings.EqualFold(strings.TrimSpace(item.Title), strings.TrimSpace(parsed.Title)) {
			continue
		}
		if parsed.Year > 0 && item.Year == parsed.Year {
			best = item
			break
		}
		if best == nil {
			best = item
		}
	}
	if best == nil {
		return nil, nil
	}
	target, err := p.target(ctx, library, *best, parsed.Sequence)
	if err != nil {
		return nil, err
	}
	return &target, nil
}

func (p *mediaUploadPlanner) target(ctx context.Context, library mediaUploadLibrary, item store.SourceItem, sequence string) (server.MediaUploadTarget, error) {
	destination, err := p.entityFolder(ctx, library, store.EntityID(item.ID), sequence)
	if err != nil {
		return server.MediaUploadTarget{}, err
	}
	// Search rows carry the generic "entity" kind; the label needs the
	// Entity's own kind.
	kind := item.Kind
	if entity, err := p.st.Entity(ctx, store.EntityID(item.ID), library.filter); err == nil {
		kind = entity.Kind
	}
	return server.MediaUploadTarget{
		EntityID:    string(item.ID),
		Title:       item.Title,
		Year:        item.Year,
		KindLabel:   p.kindLabel(library.pluginID, kind),
		Destination: destination,
	}, nil
}

// entityFolder returns the folder of the Entity's files whose parse carries
// the same sequence, else the files' common folder, else "".
func (p *mediaUploadPlanner) entityFolder(ctx context.Context, library mediaUploadLibrary, entityID store.EntityID, sequence string) (string, error) {
	members, err := p.st.MediaForEntity(ctx, entityID, library.filter)
	if err != nil {
		return "", err
	}
	if len(members) > mediaUploadMemberLimit {
		members = members[:mediaUploadMemberLimit]
	}
	var dirs []string
	for _, member := range members {
		files, err := p.st.MediaFilesForMedia(ctx, member.ID, library.filter)
		if err != nil {
			continue
		}
		for _, file := range files {
			if !library.within(filepath.Clean(file.Path)) {
				continue
			}
			dir := filepath.Dir(filepath.Clean(file.Path))
			if sequence != "" {
				if parsed := library.parse(file.Path); parsed != nil && parsed.Sequence == sequence {
					return dir, nil
				}
			}
			dirs = append(dirs, dir)
		}
	}
	if len(dirs) == 0 {
		return "", nil
	}
	common := commonMediaUploadDir(dirs)
	if parent := filepath.Dir(common); sequence != "" && allSame(dirs) && library.within(parent) && !library.isRoot(parent) {
		// Every file shares one folder below the Entity's own folder and none
		// has this sequence: the new sequence belongs beside that folder.
		common = parent
	}
	if !library.within(common) {
		return "", nil
	}
	return common, nil
}

func allSame(dirs []string) bool {
	for _, dir := range dirs[1:] {
		if dir != dirs[0] {
			return false
		}
	}
	return true
}

func commonMediaUploadDir(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	common := dirs[0]
	for _, dir := range dirs[1:] {
		for common != dir && !strings.HasPrefix(dir, common+string(filepath.Separator)) {
			parent := filepath.Dir(common)
			if parent == common {
				return ""
			}
			common = parent
		}
	}
	return common
}

func (p *mediaUploadPlanner) kindLabel(pluginID, kind string) string {
	if bundle, ok := activeBundle(p.plugins, pluginID); ok {
		for _, entityKind := range bundle.EntityKinds {
			if entityKind.Name == kind {
				if label := strings.TrimSpace(entityKind.DisplayName.Resolve("")); label != "" {
					return label
				}
			}
		}
	}
	return kind
}

func (p *mediaUploadPlanner) SearchMediaUploadTargets(ctx context.Context, filter store.LibraryFilter, libraryID store.LibraryID, query string) ([]server.MediaUploadTarget, error) {
	library, err := p.library(ctx, filter, libraryID)
	if err != nil {
		return nil, err
	}
	items, err := p.st.SearchEntities(ctx, library.filter, query, mediaUploadTargetLimit)
	if err != nil {
		return nil, err
	}
	out := make([]server.MediaUploadTarget, 0, len(items))
	for _, item := range items {
		target, err := p.target(ctx, library, item, "")
		if err != nil {
			return nil, err
		}
		if target.Destination == "" {
			name := mediaUploadFolderName(item.Title)
			if item.Year > 0 {
				name += " (" + strconv.Itoa(item.Year) + ")"
			}
			target.Destination = library.root
			if name != "" {
				target.Destination = filepath.Join(library.root, name)
			}
		}
		out = append(out, target)
	}
	return out, nil
}

func (p *mediaUploadPlanner) MediaUploadCommitted(ctx context.Context, libraryID store.LibraryID, root string) error {
	job, err := newScanJob(libraryID, root, p.now())
	if err != nil {
		return err
	}
	return p.enqueue(ctx, job)
}
