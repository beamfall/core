package scan

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
)

func requestFromJobPayload(payload []byte) (Request, error) {
	var decoded jobPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return Request{}, err
	}
	// v1 predates the backfill flag (always a normal scan); v2 carries it. Both
	// decode the same struct — a v1 payload's absent "backfill" is false.
	if decoded.Version == 1 || decoded.Version == 2 {
		return Request{
			LibraryID: decoded.LibraryID,
			Root:      decoded.Root,
			Backfill:  decoded.Backfill,
		}, nil
	}
	if decoded.Version == 0 && decoded.LibraryID == "" && decoded.Root == "" {
		var legacy Request
		if err := json.Unmarshal(payload, &legacy); err != nil {
			return Request{}, err
		}
		if legacy.LibraryID != "" || legacy.Root != "" {
			return legacy, nil
		}
	}
	return Request{}, fmt.Errorf("unsupported scan job payload version %d", decoded.Version)
}

func checkpointFromJob(payload []byte) (jobCheckpoint, error) {
	if len(payload) == 0 {
		return jobCheckpoint{Version: 1, Stage: "scanning"}, nil
	}
	var checkpoint jobCheckpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return jobCheckpoint{}, err
	}
	if checkpoint.Version == 0 && checkpoint.Stage == "" && checkpoint.LastPath == "" && checkpoint.FilesSeen == 0 && checkpoint.MediaImported == 0 {
		return jobCheckpoint{Version: 1, Stage: "scanning"}, nil
	}
	if checkpoint.Version != 1 {
		return jobCheckpoint{}, fmt.Errorf("unsupported scan checkpoint version %d", checkpoint.Version)
	}
	checkpoint.LastPath = cleanCheckpointPath(checkpoint.LastPath)
	return checkpoint, nil
}

func marshalScanCheckpoint(checkpoint jobCheckpoint) ([]byte, error) {
	checkpoint.Version = 1
	checkpoint.LastPath = cleanCheckpointPath(checkpoint.LastPath)
	return json.Marshal(checkpoint)
}

func (r Result) checkpoint(stage string) jobCheckpoint {
	return jobCheckpoint{
		Version:            1,
		Stage:              stage,
		LastPath:           r.LastPath,
		FilesSeen:          r.FilesSeen,
		TotalFiles:         r.totalFiles,
		MediaImported:      r.MediaImported,
		MissingCredentials: append([]string(nil), r.MissingCredentials...),
		FilesAdded:         r.FilesAdded,
		FilesChanged:       r.FilesChanged,
		MediaUnmatched:     r.MediaUnmatched,
		UnmatchedRecords:   slices.Sorted(maps.Keys(r.unmatchedRecords)),
	}
}

// setOf is the set of ids, or nil when there are none.
func setOf(ids []string) map[string]bool {
	if len(ids) == 0 {
		return nil
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

func cleanCheckpointPath(path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func normalizeRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
