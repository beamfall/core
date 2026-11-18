package server

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/beamfall/core/internal/capabilities"
)

// devBuildsFileEnv names the operator's runtime dev-build catalog file
// (CORE-2396). It is opt-in: unset, the server admits only the builds its
// release linked in, exactly as before.
const devBuildsFileEnv = "BEAMFALL_DEV_BUILDS_FILE"

// devBuildsRecheckInterval bounds how often a handshake or enrollment stats the
// dev-build file, so a busy server does not stat it on every request.
const devBuildsRecheckInterval = 2 * time.Second

// maxDevBuildsFileBytes bounds the dev-build file read.
const maxDevBuildsFileBytes = 1 << 20

// devBuildsFile keeps the compatibility policy's runtime dev records in step
// with an operator-owned file on the server's disk. The file is the operator
// gate: only someone who can write the server's data volume and set its
// environment can add a record, and every load is logged with the build ids it
// admits. Records are parsed by capabilities.ParseDevBuildManifest, so only
// "-dev" build ids with direct distribution and viewer or manager roles load,
// and a record never replaces a release record. A missing, unreadable or
// invalid file installs an empty dev catalog: it fails closed.
type devBuildsFile struct {
	path   string
	compat *compatState
	now    func() time.Time

	mu        sync.Mutex
	checkedAt time.Time
	modTime   time.Time
	size      int64
	present   bool
	digest    [sha256.Size]byte
	loaded    bool
}

func newDevBuildsFile(path string, compat *compatState) *devBuildsFile {
	path = strings.TrimSpace(path)
	if path == "" || compat == nil {
		return nil
	}
	return &devBuildsFile{path: path, compat: compat, now: time.Now}
}

// refresh reloads the file when it changed since the last check. It is cheap
// to call on every handshake: it stats the file at most once per
// devBuildsRecheckInterval and reparses only when the stat changes.
func (f *devBuildsFile) refresh() {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.loaded && now.Sub(f.checkedAt) < devBuildsRecheckInterval {
		return
	}
	f.checkedAt = now
	info, err := os.Stat(f.path)
	if err != nil {
		if f.loaded && !f.present {
			return
		}
		f.present = false
		f.modTime, f.size = time.Time{}, 0
		f.install(nil, nil, err)
		return
	}
	if f.loaded && f.present && info.ModTime().Equal(f.modTime) && info.Size() == f.size {
		return
	}
	f.present, f.modTime, f.size = true, info.ModTime(), info.Size()
	data, err := readBoundedFile(f.path, maxDevBuildsFileBytes)
	if err != nil {
		f.install(nil, nil, err)
		return
	}
	digest := sha256.Sum256(data)
	if f.loaded && digest == f.digest {
		return
	}
	f.digest = digest
	catalog, err := capabilities.ParseDevBuildManifest(data)
	f.install(&catalog, data, err)
}

// install hands the parsed catalog (or, on any error, an empty one) to the
// compatibility policy and logs the outcome. Callers hold f.mu.
func (f *devBuildsFile) install(catalog *capabilities.BuildCatalog, data []byte, err error) {
	f.loaded = true
	if err != nil || catalog == nil {
		if data == nil {
			f.digest = [sha256.Size]byte{}
		}
		if errors.Is(err, fs.ErrNotExist) {
			slog.Warn("dev build catalog file not found; no dev builds admitted", "path", f.path)
		} else {
			slog.Error("dev build catalog file refused; no dev builds admitted", "path", f.path, "err", err)
		}
		f.compat.setDevBuilds(capabilities.BuildCatalog{})
		return
	}
	ids := make([]string, 0, len(catalog.Builds))
	for _, record := range catalog.Builds {
		ids = append(ids, record.BuildID+" "+string(record.Surface)+" "+record.ArtifactHash[:12])
	}
	slog.Info("dev build catalog loaded", "path", f.path, "count", len(ids), "builds", ids)
	f.compat.setDevBuilds(*catalog)
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-set BEAMFALL_DEV_BUILDS_FILE path.
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("dev build catalog file exceeds %d bytes", limit)
	}
	return data, nil
}
