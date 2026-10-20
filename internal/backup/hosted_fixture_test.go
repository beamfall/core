package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestHostedCloudOpaqueFixtureRestoresWithKeyAndCiphertextOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	key, err := MintBackupKey()
	if err != nil {
		t.Fatal(err)
	}

	src := newMemBucket()
	srcSvc := NewService(src)
	backupID := "hosted-cloud/opaque-fixture"
	want := []byte("restore me from opaque hosted ciphertext")
	if err := srcSvc.Backup(ctx, key, backupID, bytes.NewReader(want)); err != nil {
		t.Fatal(err)
	}

	fixture := struct {
		Key             string `json:"key"`
		CiphertextBytes []byte `json:"ciphertext_bytes"`
	}{
		Key:             bucketKey(backupID),
		CiphertextBytes: append([]byte(nil), src.rawBytes(backupID)...),
	}
	assertHostedFixtureOpaque(t, fixture)

	hosted := newMemBucket()
	hosted.objects[fixture.Key] = fixture.CiphertextBytes
	var got bytes.Buffer
	if err := NewService(hosted).Restore(ctx, key, backupID, &got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("restored %q, want %q", got.Bytes(), want)
	}
}

func assertHostedFixtureOpaque(t *testing.T, fixture any) {
	t.Helper()
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Fatalf("hosted fixture fields = %v, want key and ciphertext_bytes only", fields)
	}
	for _, name := range []string{"key", "ciphertext_bytes"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("hosted fixture missing %q", name)
		}
	}
	for name := range fields {
		for _, marker := range []string{"account", "entitlement", "library", "playback", "watch", "media"} {
			if strings.Contains(name, marker) {
				t.Fatalf("hosted fixture leaks metadata field %q", name)
			}
		}
	}
}
