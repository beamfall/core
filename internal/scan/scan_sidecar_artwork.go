package scan

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// LocalArtworkImporter imports curated sidecar image files into Media artwork
// slots and releases a slot's local lock when its sidecar disappears. The
// artwork service implements it; scan discovers it on the ImageDeriver it is
// handed, so composition needs no new wiring. root is the absolute library
// root the sidecar must be read through; observedAt is when the scan began
// looking for sidecars, so a release never undoes a newer import's lock.
type LocalArtworkImporter interface {
	ImportLocalArtwork(ctx context.Context, id store.MediaID, slot, root, path string) (bool, error)
	ReleaseLocalArtwork(ctx context.Context, id store.MediaID, slot string, observedAt time.Time) error
}

// ContainerArtworkImporter is the Entity counterpart of LocalArtworkImporter,
// used for ordered container artwork.
type ContainerArtworkImporter interface {
	ImportEntityLocalArtwork(ctx context.Context, id store.EntityID, slot, root, path string) (bool, error)
	ReleaseEntityLocalArtwork(ctx context.Context, id store.EntityID, slot string, observedAt time.Time) error
}

// ContainerEntity is one container Entity holding a Media. Ordinal is its
// sort order inside the next container out, and Ordered says whether it has
// one.
type ContainerEntity struct {
	ID      store.EntityID
	Ordinal int
	Ordered bool
}

// ContainerEntityResolver is the optional seam that maps a Media to the
// container Entities holding it, innermost first. Identification creates those
// Entities, so an Identifier may implement it; until one does, container
// sidecars are discovered but not imported.
type ContainerEntityResolver interface {
	ContainerEntitiesForMedia(ctx context.Context, id store.MediaID) ([]ContainerEntity, error)
}

// PluginMediaClassResolver is the optional seam that reports the Media classes
// a Plugin declares. Media class is all core knows about a Plugin's content, so
// a Library whose Plugin declares the image class holds images as content and
// keeps sidecar-named images as items (CORE-0043). The Identifier may
// implement it; without it every Library skips recognised sidecars.
type PluginMediaClassResolver interface {
	PluginMediaClasses(pluginID string) []store.MediaClass
}

// ImageRule is what a Library's scan does with still images, decided by the
// Media classes its Plugin declares (CORE-0043, CORE-2361). The scan, the
// rescan preview and the first-scan estimate share it (CORE-2180).
type ImageRule int

const (
	// ImagesBesideMedia catalogues every image except recognised sidecar
	// artwork. It is the rule for a Library with no Plugin, or with a Plugin
	// whose classes are unknown (unregistered, inactive or undeclared).
	ImagesBesideMedia ImageRule = iota
	// ImagesAsContent catalogues every image as an item: the Plugin declares
	// the image class.
	ImagesAsContent
	// ImagesAsArtwork catalogues no image: the Plugin declares classes but not
	// image, so an image there is a sidecar artwork candidate or is ignored.
	ImagesAsArtwork
)

// ImageRuleFor returns the rule for a Plugin declaring classes. A Plugin that
// declares none is unknown and keeps ImagesBesideMedia.
func ImageRuleFor(classes []store.MediaClass) ImageRule {
	switch {
	case len(classes) == 0:
		return ImagesBesideMedia
	case slices.Contains(classes, store.MediaClassImage):
		return ImagesAsContent
	default:
		return ImagesAsArtwork
	}
}

// skips reports whether the scan leaves the file at path out of the catalogue
// under r, whatever kind of media file it is.
func (r ImageRule) skips(path string) bool {
	switch r {
	case ImagesAsContent:
		return false
	case ImagesAsArtwork:
		return imageClassForPath(path)
	default:
		return isLocalArtworkSidecar(path)
	}
}

// Local artwork scopes. A file-scope name is "<stem>-<name>" for the Media
// whose file stem it carries; a folder-scope name applies when the folder holds
// exactly one non-image media file; a folder-dir scope name is a subdirectory
// whose image files are lower-ranked candidates; a container-scope name targets
// the ordered container Entity.
const (
	localArtworkFile      = "file"
	localArtworkFolder    = "folder"
	localArtworkFolderDir = "folder-dir"
	localArtworkContainer = "container"
)

// localArtworkName is one row of the local artwork vocabulary. Slot is empty
// for a name that is recognised but has no importable slot yet.
type localArtworkName struct {
	Scope string
	Name  string
	Slot  string
}

// localArtworkVocabulary is the single lookup table of curated sidecar names
// (CORE-0359). Within a slot, earlier rows win. logo, clearlogo and banner are
// recognised but not imported: the slot files are JPEG, which would drop logo
// transparency, so they wait for a follow-up. The "season00-poster" row stands
// for any two or more digits, parsed as the container ordinal. The container
// banner, fanart, landscape and "season-all" names are recognised so the walk
// never catalogues them as items (CORE-2313), but only the posters fill a slot.
var localArtworkVocabulary = []localArtworkName{
	{localArtworkFile, "poster", "poster"},
	{localArtworkFile, "fanart", "backdrop"},
	{localArtworkFile, "thumb", "thumb"},
	{localArtworkFile, "landscape", "thumb"},
	{localArtworkFile, "logo", ""},
	{localArtworkFile, "banner", ""},
	{localArtworkFolder, "poster", "poster"},
	{localArtworkFolder, "folder", "poster"},
	{localArtworkFolder, "cover", "poster"},
	{localArtworkFolder, "fanart", "backdrop"},
	{localArtworkFolder, "backdrop", "backdrop"},
	{localArtworkFolder, "thumb", "thumb"},
	{localArtworkFolder, "landscape", "thumb"},
	{localArtworkFolder, "banner", ""},
	{localArtworkFolder, "logo", ""},
	{localArtworkFolder, "clearlogo", ""},
	{localArtworkFolder, "artist", ""},
	{localArtworkFolderDir, "extrafanart", "backdrop"},
	{localArtworkContainer, "season00-poster", "poster"},
	{localArtworkContainer, "season-specials-poster", "poster"},
	{localArtworkContainer, "season00-banner", ""},
	{localArtworkContainer, "season00-fanart", ""},
	{localArtworkContainer, "season00-landscape", ""},
	{localArtworkContainer, "season00-thumb", ""},
	{localArtworkContainer, "season-specials-banner", ""},
	{localArtworkContainer, "season-specials-fanart", ""},
	{localArtworkContainer, "season-specials-landscape", ""},
	{localArtworkContainer, "season-specials-thumb", ""},
	{localArtworkContainer, "season-all-poster", ""},
	{localArtworkContainer, "season-all-banner", ""},
	{localArtworkContainer, "season-all-fanart", ""},
	{localArtworkContainer, "season-all-landscape", ""},
	{localArtworkContainer, "season-all-thumb", ""},
}

// localArtworkExts is the accepted sidecar image formats, in preference order.
var localArtworkExts = []string{"jpg", "jpeg", "png", "webp"}

// isLocalArtworkSidecar reports whether path is an image the local artwork
// vocabulary recognises, so the walk never catalogues it as an item
// (CORE-1708). Folder and container names match on the name alone; a file
// name "<stem>-<name>" matches only beside a non-image media file with that
// stem, and any image inside an extrafanart directory matches. It reads at most
// the file's own directory and keeps no state, so the walker and the consumer
// may call it concurrently.
func isLocalArtworkSidecar(path string) bool {
	lower := strings.ToLower(filepath.Base(path))
	ext := strings.TrimPrefix(filepath.Ext(lower), ".")
	if !slices.Contains(localArtworkExts, ext) {
		return false
	}
	base := strings.TrimSuffix(lower, "."+ext)
	if base == "" {
		return false
	}
	var fileStems []string
	for _, row := range localArtworkVocabulary {
		switch row.Scope {
		case localArtworkFolder:
			if base == row.Name {
				return true
			}
		case localArtworkFolderDir:
			if strings.EqualFold(filepath.Base(filepath.Dir(path)), row.Name) {
				return true
			}
		case localArtworkContainer:
			if containerArtworkName(base, row.Name) {
				return true
			}
		case localArtworkFile:
			if stem, ok := strings.CutSuffix(base, "-"+row.Name); ok && stem != "" {
				fileStems = append(fileStems, stem)
			}
		}
	}
	return len(fileStems) > 0 && dirHasMediaStem(filepath.Dir(path), fileStems)
}

// containerArtworkName reports whether base matches a container vocabulary
// name, where "00" stands for two or more digits.
func containerArtworkName(base, name string) bool {
	prefix, suffix, numbered := strings.Cut(name, "00")
	if !numbered {
		return base == name
	}
	digits, ok := strings.CutPrefix(base, prefix)
	if !ok {
		return false
	}
	digits, ok = strings.CutSuffix(digits, suffix)
	if !ok || len(digits) < 2 {
		return false
	}
	return strings.Trim(digits, "0123456789") == ""
}

// dirHasMediaStem reports whether dir holds a non-image media file whose
// lowercase stem is one of stems.
func dirHasMediaStem(dir string, stems []string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		if entry.IsDir() || !mediaExtensions[ext] || imageExtensions[ext] {
			continue
		}
		if slices.Contains(stems, strings.ToLower(titleFromPath(name))) {
			return true
		}
	}
	return false
}

// localArtworkMediaSlots lists the Media slots local artwork can fill, in a
// stable order.
func localArtworkMediaSlots() []string {
	var slots []string
	seen := map[string]bool{}
	for _, row := range localArtworkVocabulary {
		if row.Slot == "" || row.Scope == localArtworkContainer || seen[row.Slot] {
			continue
		}
		seen[row.Slot] = true
		slots = append(slots, row.Slot)
	}
	return slots
}

// localArtworkDir indexes one directory's sidecar candidates.
type localArtworkDir struct {
	files       map[string]string // lowercase name -> on-disk name, regular files only
	mediaFiles  int               // non-image media files
	extrafanart string            // on-disk name of a real extrafanart directory
}

func (d *sidecarDiscovery) artworkDir(dir string) *localArtworkDir {
	if cached, ok := d.artwork[dir]; ok {
		return cached
	}
	index := &localArtworkDir{files: map[string]string{}}
	for _, entry := range d.entries(dir) {
		name := entry.Name()
		lower := strings.ToLower(name)
		if entry.IsDir() {
			if lower == "extrafanart" && index.extrafanart == "" {
				index.extrafanart = name
			}
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		index.files[lower] = name
		ext := strings.TrimPrefix(filepath.Ext(lower), ".")
		if mediaExtensions[ext] && !imageExtensions[ext] && titleFromPath(name) != "" {
			index.mediaFiles++
		}
	}
	d.artwork[dir] = index
	return index
}

// find returns the on-disk path of base in any accepted image format.
func (d *localArtworkDir) find(dir, base string) string {
	for _, ext := range localArtworkExts {
		if name, ok := d.files[base+"."+ext]; ok {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// firstExtrafanart returns the lexicographically first accepted image in a
// real extrafanart directory. Ordered multiple backdrops are a follow-up.
func (d *sidecarDiscovery) firstExtrafanart(dir string, index *localArtworkDir) string {
	if index.extrafanart == "" {
		return ""
	}
	sub := filepath.Join(dir, index.extrafanart)
	for _, entry := range d.entries(sub) {
		if !entry.Type().IsRegular() {
			continue
		}
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(entry.Name())), ".")
		for _, allowed := range localArtworkExts {
			if ext == allowed && titleFromPath(entry.Name()) != "" {
				return filepath.Join(sub, entry.Name())
			}
		}
	}
	return ""
}

// localArtworkFor returns the winning sidecar path per Media slot for one
// non-image media file. A slot with no candidate is absent from the map.
func (d *sidecarDiscovery) localArtworkFor(mediaPath string) map[string]string {
	dir := filepath.Dir(mediaPath)
	index := d.artworkDir(dir)
	stem := strings.ToLower(titleFromPath(mediaPath))
	out := map[string]string{}
	for _, row := range localArtworkVocabulary {
		if row.Slot == "" || out[row.Slot] != "" {
			continue
		}
		var found string
		switch row.Scope {
		case localArtworkFile:
			if stem != "" {
				found = index.find(dir, stem+"-"+row.Name)
			}
		case localArtworkFolder:
			if index.mediaFiles == 1 {
				found = index.find(dir, row.Name)
			}
		case localArtworkFolderDir:
			if index.mediaFiles == 1 {
				found = d.firstExtrafanart(dir, index)
			}
		}
		if found != "" {
			out[row.Slot] = found
		}
	}
	return out
}

// containerArtworkIn returns the best container sidecar for ordinal in dir:
// the numbered name before the named one, then format preference.
func (d *sidecarDiscovery) containerArtworkIn(dir string, ordinal int) string {
	index := d.artworkDir(dir)
	for _, row := range localArtworkVocabulary {
		if row.Scope != localArtworkContainer || row.Slot == "" {
			continue
		}
		if !strings.Contains(row.Name, "00") {
			if ordinal == 0 {
				if found := index.find(dir, row.Name); found != "" {
					return found
				}
			}
			continue
		}
		prefix, suffix, _ := strings.Cut(row.Name, "00")
		for _, width := range []int{2, 3, 4} {
			digits := strconv.Itoa(ordinal)
			if len(digits) > width {
				continue
			}
			digits = strings.Repeat("0", width-len(digits)) + digits
			if found := index.find(dir, prefix+digits+suffix); found != "" {
				return found
			}
		}
	}
	return ""
}

// folderPosterIn returns the best folder poster in dir (poster, folder, then
// cover, each by format preference), whatever the folder's media count: it
// names the container the folder is.
func (d *sidecarDiscovery) folderPosterIn(dir string) string {
	index := d.artworkDir(dir)
	for _, row := range localArtworkVocabulary {
		if row.Scope != localArtworkFolder || row.Slot != "poster" {
			continue
		}
		if found := index.find(dir, row.Name); found != "" {
			return found
		}
	}
	return ""
}

// localArtSlot names one Media artwork slot for the walk's deferred releases.
type localArtSlot struct {
	media store.MediaID
	slot  string
}

// containerArtChoice is the per-scan container sidecar decision for one Entity.
// An empty path asks for a release of any lock written before the scan began.
type containerArtChoice struct {
	path string
}

// stableSidecar reports whether a sidecar is settled (not still being written).
func stableSidecar(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && now().Sub(info.ModTime()) >= stabilityWindow
}

// importPaths returns the absolute library root and sidecar path an importer
// reads through.
func (s *scanWalkState) importPaths(sidecar string) (string, string, error) {
	root, err := filepath.Abs(s.req.Root)
	if err != nil {
		return "", "", err
	}
	abs, err := filepath.Abs(sidecar)
	return root, abs, err
}

// insideRoot reports whether dir is the scan root or below it.
func (s *scanWalkState) insideRoot(dir string) bool {
	rel, err := filepath.Rel(s.req.Root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// discSubdirPattern matches per-disc folder names ("CD 1", "CD01",
// "Disc 2", "Disk 1") inside multi-disc album folders.
var discSubdirPattern = regexp.MustCompile(`(?i)^(?:cd|dis[ck])[\s._-]*0*\d{1,2}$`)

// applyLocalArtwork imports or releases the local artwork of one completed
// non-image Media. It runs on every scan, fast path included, so an edited
// sidecar next to an unchanged media file still re-imports. A slot with no
// candidate is queued for release; applyDeferredArtwork releases it unless
// another file of the same Media matched that slot in this walk. Failures are
// logged and never fail the scan.
func (s *scanWalkState) applyLocalArtwork(mediaPath string, mediaID store.MediaID) {
	s.applyMediaLocalArtwork(mediaPath, mediaID)
	s.noteContainerArtwork(mediaPath, mediaID)
}

// applyMediaLocalArtwork imports curated sidecar files into Media slots (or
// queues release when absent). For a Media held directly by a single unordered
// container Entity (such as an album holding tracks), a folder poster sidecar
// (poster, folder or cover) fills SlotPoster when no file-level poster matched.
func (s *scanWalkState) applyMediaLocalArtwork(mediaPath string, mediaID store.MediaID) {
	if s.localArtwork == nil || imageClassForPath(mediaPath) {
		return
	}
	found := s.sidecars.localArtworkFor(mediaPath)
	if found["poster"] == "" && s.hasSoleDirectContainer(s.containersFor(mediaID)) {
		if sidecar := s.directContainerFolderPoster(mediaPath); sidecar != "" {
			found["poster"] = sidecar
		}
	}
	for _, slot := range localArtworkMediaSlots() {
		key := localArtSlot{media: mediaID, slot: slot}
		sidecar := found[slot]
		if sidecar == "" {
			s.localReleases[key] = true
			continue
		}
		s.localMatched[key] = true
		if !stableSidecar(sidecar) {
			continue
		}
		root, abs, err := s.importPaths(sidecar)
		if err == nil {
			_, err = s.localArtwork.ImportLocalArtwork(s.ctx, mediaID, slot, root, abs)
		}
		if err != nil {
			slog.Warn("importing local artwork failed", "media", mediaID, "slot", slot, "error", err.Error())
		}
	}
}

func (s *scanWalkState) containersFor(mediaID store.MediaID) []ContainerEntity {
	if s.containerResolver == nil {
		return nil
	}
	containers, err := s.containerResolver.ContainerEntitiesForMedia(s.ctx, mediaID)
	if err != nil {
		slog.Warn("resolving container entity failed", "media", mediaID, "error", err.Error())
		return nil
	}
	return containers
}

// hasSoleDirectContainer reports whether the Media is held directly by a single
// unordered container Entity (rather than nested inside an ordered container
// hierarchy).
func (s *scanWalkState) hasSoleDirectContainer(containers []ContainerEntity) bool {
	return len(containers) == 1 && containers[0].ID != "" && !containers[0].Ordered
}

// directContainerFolderPoster returns the best folder poster for a Media held
// directly by a single unordered container, checking the Media's directory and
// then its parent inside the library root when the directory is a disc folder.
func (s *scanWalkState) directContainerFolderPoster(mediaPath string) string {
	dir := filepath.Dir(mediaPath)
	if s.insideRoot(dir) {
		if found := s.sidecars.folderPosterIn(dir); found != "" {
			return found
		}
	}
	parent := filepath.Dir(dir)
	if discSubdirPattern.MatchString(filepath.Base(dir)) && parent != dir && s.insideRoot(parent) {
		return s.sidecars.folderPosterIn(parent)
	}
	return ""
}

// noteContainerArtwork records the container sidecars for the Media's
// container Entities, when the resolver seam is wired: the innermost ordered
// container takes the numbered sidecar in the Media's folder or its parent, an
// enclosing outermost container takes the library root's top-level folder
// poster, and a sole direct container takes directContainerFolderPoster from the
// Media's directory (or its disc-parent inside the root). Nothing outside the
// root is read. Each Entity is written once after the walk so Media of one
// container cannot flip-flop it.
func (s *scanWalkState) noteContainerArtwork(mediaPath string, mediaID store.MediaID) {
	if s.containerResolver == nil || s.containerArtwork == nil {
		return
	}
	containers := s.containersFor(mediaID)
	numbered := store.EntityID("")
	for _, container := range containers {
		if container.Ordered && container.ID != "" {
			numbered = container.ID
			s.noteContainerChoice(container.ID, func() string {
				return s.numberedContainerArtwork(mediaPath, container.Ordinal)
			})
			break
		}
	}
	if len(containers) == 0 {
		return
	}
	outer := containers[len(containers)-1]
	if outer.ID == "" || outer.ID == numbered {
		return
	}
	if s.hasSoleDirectContainer(containers) {
		s.noteContainerChoice(outer.ID, func() string {
			return s.directContainerFolderPoster(mediaPath)
		})
		return
	}
	s.noteContainerChoice(outer.ID, func() string {
		dir := s.topLevelFolder(mediaPath)
		if dir == "" {
			return ""
		}
		return s.sidecars.folderPosterIn(dir)
	})
}

// noteContainerChoice records an Entity's container sidecar, asking find only
// until one is found in this walk.
func (s *scanWalkState) noteContainerChoice(entityID store.EntityID, find func() string) {
	choice := s.containerArt[entityID]
	if choice == nil {
		choice = &containerArtChoice{}
		s.containerArt[entityID] = choice
	}
	if choice.path == "" {
		choice.path = find()
	}
}

// numberedContainerArtwork returns the numbered container sidecar for ordinal
// in the Media's folder, then its parent when that is inside the root.
func (s *scanWalkState) numberedContainerArtwork(mediaPath string, ordinal int) string {
	dir := filepath.Dir(mediaPath)
	dirs := []string{dir}
	parent := filepath.Dir(dir)
	if parent != dir && s.insideRoot(parent) {
		dirs = append(dirs, parent)
	}
	for _, candidate := range dirs {
		if found := s.sidecars.containerArtworkIn(candidate, ordinal); found != "" {
			return found
		}
	}
	return ""
}

// topLevelFolder returns the library root's direct child folder holding
// mediaPath, or "" for a Media directly in the root or outside it.
func (s *scanWalkState) topLevelFolder(mediaPath string) string {
	rel, err := filepath.Rel(s.req.Root, filepath.Dir(mediaPath))
	if err != nil || rel == "." || !s.insideRoot(filepath.Dir(mediaPath)) {
		return ""
	}
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	return filepath.Join(s.req.Root, filepath.FromSlash(first))
}

// applyDeferredArtwork writes the local lock releases and container sidecar
// decisions gathered so far and clears them. It runs after the walk and
// before a job yields, so a resumed scan does not lose the decisions of Media
// it will skip. A release removes only a lock written before the walk began
// (the scan has seen its sidecar's directory since), so it covers a sidecar
// that was removed, renamed or now shared by two media files, and a pending
// marker left by an interrupted import.
func (s *scanWalkState) applyDeferredArtwork() {
	for key := range s.localReleases {
		if s.localMatched[key] {
			continue
		}
		if err := s.localArtwork.ReleaseLocalArtwork(s.ctx, key.media, key.slot, s.startedAt); err != nil {
			slog.Warn("releasing local artwork failed", "media", key.media, "slot", key.slot, "error", err.Error())
		}
	}
	clear(s.localReleases)
	clear(s.localMatched)
	for entityID, choice := range s.containerArt {
		if choice.path == "" {
			if err := s.containerArtwork.ReleaseEntityLocalArtwork(s.ctx, entityID, "poster", s.startedAt); err != nil {
				slog.Warn("releasing container artwork failed", "entity", entityID, "error", err.Error())
			}
			continue
		}
		if !stableSidecar(choice.path) {
			continue
		}
		root, abs, err := s.importPaths(choice.path)
		if err == nil {
			_, err = s.containerArtwork.ImportEntityLocalArtwork(s.ctx, entityID, "poster", root, abs)
		}
		if err != nil {
			slog.Warn("importing container artwork failed", "entity", entityID, "error", err.Error())
		}
	}
	clear(s.containerArt)
}

// flushDeferredArtworkOnYield applies the artwork decisions gathered so far
// when the job yields, then returns err unchanged.
func (s *scanWalkState) flushDeferredArtworkOnYield(err error) error {
	if errors.Is(err, jobs.ErrYielded) {
		s.applyDeferredArtwork()
	}
	return err
}
