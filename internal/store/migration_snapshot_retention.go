package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	migrationSnapshotSoakBoots = 3
	migrationSnapshotKeep      = 2
	migrationSoakStateFile     = ".soak.json"
)

type migrationSoakState struct {
	SessionID       string `json:"sessionId"`
	SuccessfulBoots int    `json:"successfulBoots"`
}

type migrationSnapshotEntry struct {
	path      string
	manifest  migrationSessionManifest
	createdAt time.Time
}

func recordSuccessfulMigrationBoot(dataDir string) error {
	backupDir := filepath.Join(dataDir, "backups", "pre-migration")
	snapshots, err := migrationSnapshots(backupDir)
	if os.IsNotExist(err) || len(snapshots) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	latest := snapshots[len(snapshots)-1]
	statePath := filepath.Join(backupDir, migrationSoakStateFile)
	state := migrationSoakState{}
	raw, err := os.ReadFile(statePath) //nolint:gosec // fixed state path under dataDir
	if err == nil {
		_ = json.Unmarshal(raw, &state)
	} else if !os.IsNotExist(err) {
		return err
	}
	if state.SessionID != latest.manifest.SessionID {
		state = migrationSoakState{SessionID: latest.manifest.SessionID}
	}
	state.SuccessfulBoots++
	raw, err = json.Marshal(state)
	if err != nil {
		return err
	}
	if err := writeFileSync0600(statePath, append(raw, '\n')); err != nil {
		return err
	}
	if state.SuccessfulBoots < migrationSnapshotSoakBoots {
		return nil
	}
	for len(snapshots) > migrationSnapshotKeep {
		oldest := snapshots[0]
		if oldest.manifest.SessionID == latest.manifest.SessionID {
			return errors.New("migration snapshot retention selected newest session")
		}
		if err := os.RemoveAll(oldest.path); err != nil {
			return err
		}
		snapshots = snapshots[1:]
	}
	return syncDir(backupDir)
}

func migrationSnapshots(backupDir string) ([]migrationSnapshotEntry, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, err
	}
	snapshots := []migrationSnapshotEntry{}
	for _, entry := range entries {
		if !entry.IsDir() || !stringsHasSessionPrefix(entry.Name()) {
			continue
		}
		path := filepath.Join(backupDir, entry.Name())
		raw, err := os.ReadFile(filepath.Join(path, backupManifestBasename)) //nolint:gosec // fixed manifest under backupDir
		if err != nil {
			continue
		}
		var manifest migrationSessionManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			continue
		}
		if manifest.Type != migrationSnapshotType || entry.Name() != "session-"+manifest.SessionID {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt)
		if err != nil {
			continue
		}
		snapshots = append(snapshots, migrationSnapshotEntry{path: path, manifest: manifest, createdAt: createdAt})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].createdAt.Before(snapshots[j].createdAt)
	})
	return snapshots, nil
}

func stringsHasSessionPrefix(name string) bool {
	return len(name) > len("session-") && name[:len("session-")] == "session-"
}
