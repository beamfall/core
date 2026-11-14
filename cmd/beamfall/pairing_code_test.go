package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"regexp"
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
			if len(lines) != 2 || !strings.Contains(lines[1], "http://127.0.0.1:8800/web/") {
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

// TestPairingCodeOutput covers CORE-1609: a real URL for the configured
// address, a relative and local-time expiry, and no "start the server".
func TestPairingCodeOutput(t *testing.T) {
	dataDir, _ := ownerDataDir(t)
	t.Setenv("BEAMFALL_ADDR", "127.0.0.1:9123")
	var out bytes.Buffer
	if err := pairingCodeCommand(context.Background(), []string{"--data-dir", dataDir}, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := regexp.MustCompile(`^Open http://127\.0\.0\.1:9123/web/ and enter this code within \d+ minutes \(it expires at \d\d:\d\d\)\.$`)
	if len(lines) != 2 || !want.MatchString(lines[1]) {
		t.Fatalf("output = %q, want the code then one instruction line", out.String())
	}
	if strings.Contains(strings.ToLower(out.String()), "start the server") {
		t.Fatalf("output tells the owner to start a server: %q", out.String())
	}
}

func TestPairingWebURL(t *testing.T) {
	lan := []net.Addr{
		&net.IPNet{IP: net.IPv4(127, 0, 0, 1), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.IPv4(169, 254, 1, 2), Mask: net.CIDRMask(16, 32)},
		&net.IPNet{IP: net.IPv4(192, 168, 1, 20), Mask: net.CIDRMask(24, 32)},
	}
	for _, tc := range []struct {
		addr  string
		addrs []net.Addr
		want  string
	}{
		{addr: "127.0.0.1:8800", addrs: lan, want: "http://127.0.0.1:8800/web/"},
		{addr: "0.0.0.0:9000", addrs: lan, want: "http://192.168.1.20:9000/web/"},
		{addr: ":9000", addrs: nil, want: "http://127.0.0.1:9000/web/"},
		{addr: "10.0.0.5:8800", addrs: lan, want: "http://10.0.0.5:8800/web/"},
	} {
		if got := pairingWebURL(tc.addr, tc.addrs); got != tc.want {
			t.Errorf("pairingWebURL(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}

func TestPairingExpiry(t *testing.T) {
	now := time.Date(2026, 11, 14, 16, 1, 18, 0, time.UTC)
	at := now.Add(10 * time.Minute)
	want := "within 10 minutes (it expires at " + at.Local().Format("15:04") + ")"
	if got := pairingExpiry(at.Format(time.RFC3339), now); got != want {
		t.Fatalf("pairingExpiry = %q, want %q", got, want)
	}
}

// TestPairingCodeDataDirResolution covers CORE-1599: a relative --data-dir and
// the default .beamfall resolve against the working directory, and a path with
// no Beamfall data is refused without creating anything.
func TestPairingCodeDataDirResolution(t *testing.T) {
	t.Setenv("BEAMFALL_DATA_DIR", "")
	owned, _ := ownerDataDir(t)
	parent := t.TempDir()
	for _, dir := range []string{"data-a", ".beamfall"} {
		if err := os.Rename(owned, filepath.Join(parent, dir)); err != nil {
			t.Fatal(err)
		}
		owned, _ = ownerDataDir(t)
	}
	t.Chdir(parent)
	for _, args := range [][]string{{"--data-dir", "data-a"}, nil} {
		var out bytes.Buffer
		if err := pairingCodeCommand(context.Background(), args, &out); err != nil {
			t.Fatalf("pairing-code %v: %v", args, err)
		}
		if out.Len() == 0 {
			t.Fatalf("pairing-code %v printed no code", args)
		}
	}
	empty := t.TempDir()
	t.Chdir(empty)
	missing := filepath.Join(parent, "typo")
	for _, args := range [][]string{{"--data-dir", missing}, nil} {
		err := pairingCodeCommand(context.Background(), args, nil)
		if err == nil || !strings.Contains(err.Error(), "no Beamfall data in ") {
			t.Fatalf("pairing-code %v error = %v, want no Beamfall data", args, err)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing data dir was created: %v", err)
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Fatalf("default data dir was created in %s", empty)
	}
}

func TestPairingCodeRefusals(t *testing.T) {
	locked, _ := ownerDataDir(t)
	holdServerLockForTest(t, locked)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "server running", args: []string{"--data-dir", locked}, want: "server appears to be running"},
		{name: "no owner", args: []string{"--data-dir", existingDataDir(t)}, want: "no owner profile"},
		{name: "no data", args: []string{"--data-dir", t.TempDir()}, want: "no Beamfall data in "},
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
