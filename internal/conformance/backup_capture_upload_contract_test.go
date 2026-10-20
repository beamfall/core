package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestBackupCaptureUploadContract(t *testing.T) {
	root := repoRoot(t)
	fixtures := readBackupCaptureJSON(t, filepath.Join(root, "contracts/backup/capture-upload/v1/fixtures.json"))
	if fixtures["contractVersion"] != "1.0.0" {
		t.Fatalf("contractVersion = %v, want 1.0.0", fixtures["contractVersion"])
	}
	for _, fixture := range fixtures["fixtures"].([]any) {
		f := fixture.(map[string]any)
		endpoint := f["endpoint"].(map[string]any)
		if endpoint["method"] != "POST" || endpoint["path"] != "/v1/backups/media" || endpoint["scope"] != "backup" || endpoint["profileBound"] != true {
			t.Fatalf("%s endpoint drifted: %#v", f["name"], endpoint)
		}
		required := backupCaptureStrings(endpoint["multipartRequired"].([]any))
		if !slices.Contains(required, "libraryID") || !slices.Contains(required, "file") {
			t.Fatalf("%s multipartRequired = %v, want libraryID and file", f["name"], required)
		}
		queue := f["queue"].(map[string]any)
		if queue["backgroundTransfer"] != true || queue["resumable"] != true {
			t.Fatalf("%s lost background/resumable contract: %#v", f["name"], queue)
		}
		deleteGate := queue["deleteAfterBackup"].(map[string]any)
		if deleteGate["requiresConfirmedStorage"] != true {
			t.Fatalf("%s delete gate must require confirmed storage", f["name"])
		}
	}

	openapi := readBackupCaptureJSON(t, filepath.Join(root, "docs/api/openapi.json"))
	upload := openapi["paths"].(map[string]any)["/v1/backups/media"].(map[string]any)["post"].(map[string]any)
	if upload["x-required-scope"] != "backup" {
		t.Fatalf("/v1/backups/media scope = %v, want backup", upload["x-required-scope"])
	}
	form := upload["requestBody"].(map[string]any)["content"].(map[string]any)["multipart/form-data"].(map[string]any)["schema"].(map[string]any)
	required := backupCaptureStrings(form["required"].([]any))
	if !slices.Contains(required, "libraryID") || !slices.Contains(required, "file") {
		t.Fatalf("/v1/backups/media required fields = %v, want libraryID and file", required)
	}
	responses := upload["responses"].(map[string]any)
	for _, status := range []string{"201", "423", "507"} {
		if _, ok := responses[status]; !ok {
			t.Fatalf("/v1/backups/media missing %s response", status)
		}
	}
}

func readBackupCaptureJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func backupCaptureStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.(string))
	}
	return out
}
