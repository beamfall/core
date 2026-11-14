package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// ownerDataDir returns a data directory whose store has an owner Profile.
func ownerDataDir(t *testing.T) (string, store.ProfileID) {
	t.Helper()
	dataDir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, dataDir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	profile, err := st.CreateProfile(ctx, "Owner", store.ProfileAdult, store.LibraryFilter{Revealed: true, AllLibraries: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetProfileOwner(ctx, profile.ID); err != nil {
		t.Fatal(err)
	}
	return dataDir, profile.ID
}

func TestPairingCodeMintsOwnerCode(t *testing.T) {
	for _, tc := range []struct {
		args []string
		role store.DeviceRole
	}{
		{args: nil, role: store.DeviceRoleManager},
		{args: []string{"--role", "viewer"}, role: store.DeviceRoleViewer},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			dataDir, ownerID := ownerDataDir(t)
			var out bytes.Buffer
			if err := pairingCodeCommand(context.Background(), append([]string{"--data-dir", dataDir}, tc.args...), &out); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != 2 || !strings.Contains(lines[1], "http://<server>:8800/web/") {
				t.Fatalf("output = %q, want a code and one instruction line", out.String())
			}
			st, err := store.Open(context.Background(), dataDir, store.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			code, err := st.PairingCode(context.Background(), lines[0], time.Now().UTC())
			if err != nil {
				t.Fatalf("minted code %q not found: %v", lines[0], err)
			}
			if code.ProfileID != ownerID || code.DeviceRole != tc.role {
				t.Fatalf("code = %+v, want owner %s role %s", code, ownerID, tc.role)
			}
		})
	}
}

func TestPairingCodeHonoursDataDirEnv(t *testing.T) {
	dataDir, _ := ownerDataDir(t)
	t.Setenv("BEAMFALL_DATA_DIR", dataDir)
	var out bytes.Buffer
	if err := pairingCodeCommand(context.Background(), nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		t.Fatal("no code printed")
	}
}

func TestPairingCodeRefusals(t *testing.T) {
	locked, _ := ownerDataDir(t)
	if err := os.WriteFile(filepath.Join(locked, "server.lock"), []byte("running\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "server running", args: []string{"--data-dir", locked}, want: "server appears to be running"},
		{name: "no owner", args: []string{"--data-dir", t.TempDir()}, want: "no owner profile"},
		{name: "bad role", args: []string{"--data-dir", t.TempDir(), "--role", "admin"}, want: "--role must be manager or viewer"},
		{name: "extra argument", args: []string{"--data-dir", t.TempDir(), "now"}, want: "unexpected argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := pairingCodeCommand(context.Background(), tc.args, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
