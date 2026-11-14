package scan

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beamfall/core/internal/jobs"
	"github.com/beamfall/core/internal/store"
)

// LocalArtworkImporter imports curated sidecar image files into Media artwork
// slots and releases a slot's local lock when its sidecar disappears. The
// artwork service implements it; scan discovers it on the ImageDeriver it is
// handed, so composition needs no new wiring. root is the absolute library
// root the sidecar must be read through.
type LocalArtworkImporter interface {
	ImportLocalArtwork(ctx context.Context, id store.MediaID, slot, root, path string) (bool, error)
	ReleaseLocalArtwork(ctx context.Context, id store.MediaID, slot string) error
}

// ContainerArtworkImporter is the Entity counterpart of LocalArtworkImporter,
// used for ordered container artwork.
type ContainerArtworkImporter interface {
	ImportEntityLocalArtwork(ctx context.Context, id store.EntityID, slot, root, path string) (bool, error)
	ReleaseEntityLocalArtwork(ctx context.Context, id store.EntityID, slot string) error
}

// ContainerEntityResolver is the optional seam that maps a Media to the ordered
// container Entity it belongs to and that container's ordinal. Identification
// creates those Entities, so an Identifier may implement it; until one does,
// container sidecars are discovered but not imported.
type ContainerEntityResolver interface {
	ContainerEntityForMedia(ctx context.Context, id store.MediaID) (store.EntityID, int, bool, error)
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
// for any two or more digits, parsed as the container ordinal.
var localArtworkVocabulary = []localArtworkName{
	{localArtworkFile, "poster", "poster"},
	{localArtworkFile, "fanart", "backdrop"},
	{localArtworkFile, "thumb", "thumb"},
	{localArtworkFile, "landscape", "thumb"},
	{localArtworkFile, "logo", ""},
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
	{localArtworkFolderDir, "extrafanart", "backdrop"},
	{localArtworkContainer, "season00-poster", "poster"},
	{localArtworkContainer, "season-specials-poster", "poster"},
}

// localArtworkExts is the accepted sidecar image formats, in preference order.
var localArtworkExts = []string{"jpg", "jpeg", "png", "webp"}

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

// containerArtChoice is the per-scan container sidecar decision for one Entity.
// An empty path asks for a release, which the importer performs only once the
// sidecar its lock recorded is gone.
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

// applyLocalArtwork imports or releases the local artwork of one completed
// non-image Media. It runs on every scan, fast path included, so an edited
// sidecar next to an unchanged media file still re-imports. A slot with no
// candidate asks for a release, which keeps the lock while its recorded
// sidecar still exists. Failures are logged and never fail the scan.
func (s *scanWalkState) applyLocalArtwork(mediaPath string, mediaID store.MediaID) {
	if s.localArtwork == nil || imageClassForPath(mediaPath) {
		return
	}
	found := s.sidecars.localArtworkFor(mediaPath)
	for _, slot := range localArtworkMediaSlots() {
		sidecar := found[slot]
		if sidecar == "" {
			if err := s.localArtwork.ReleaseLocalArtwork(s.ctx, mediaID, slot); err != nil {
				slog.Warn("releasing local artwork failed", "media", mediaID, "slot", slot, "error", err.Error())
			}
			continue
		}
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
	s.noteContainerArtwork(mediaPath, mediaID)
}

// noteContainerArtwork records the container sidecar for the Media's ordered
// container Entity, when the resolver seam is wired. The Entity is written
// once after the walk so Media of one container cannot flip-flop it.
func (s *scanWalkState) noteContainerArtwork(mediaPath string, mediaID store.MediaID) {
	if s.containerResolver == nil || s.containerArtwork == nil {
		return
	}
	entityID, ordinal, ok, err := s.containerResolver.ContainerEntityForMedia(s.ctx, mediaID)
	if err != nil {
		slog.Warn("resolving container entity failed", "media", mediaID, "error", err.Error())
		return
	}
	if !ok || entityID == "" {
		return
	}
	choice := s.containerArt[entityID]
	if choice == nil {
		choice = &containerArtChoice{}
		s.containerArt[entityID] = choice
	}
	dir := filepath.Dir(mediaPath)
	dirs := []string{dir}
	parent := filepath.Dir(dir)
	if parent != dir && s.insideRoot(parent) {
		dirs = append(dirs, parent)
	}
	if choice.path != "" {
		return
	}
	for _, candidate := range dirs {
		if found := s.sidecars.containerArtworkIn(candidate, ordinal); found != "" {
			choice.path = found
			return
		}
	}
}

// applyContainerArtwork writes the container sidecar decisions gathered so
// far and clears them. It runs after the walk and before a job yields, so a
// resumed scan does not lose the decisions of Media it will skip.
func (s *scanWalkState) applyContainerArtwork() {
	for entityID, choice := range s.containerArt {
		if choice.path == "" {
			if err := s.containerArtwork.ReleaseEntityLocalArtwork(s.ctx, entityID, "poster"); err != nil {
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

// flushContainerArtworkOnYield applies the container decisions gathered so far
// when the job yields, then returns err unchanged.
func (s *scanWalkState) flushContainerArtworkOnYield(err error) error {
	if errors.Is(err, jobs.ErrYielded) {
		s.applyContainerArtwork()
	}
	return err
}
