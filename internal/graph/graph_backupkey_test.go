package graph

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// fakeBackupKeyEscrow holds a placeholder key, not a real backup key.
type fakeBackupKeyEscrow struct {
	escrowID  string
	expiresAt time.Time
}

const fakeBackupKey = "placeholder-backup-key"

func (f *fakeBackupKeyEscrow) Pending() (bool, time.Time, error) {
	return f.escrowID != "", f.expiresAt, nil
}

func (f *fakeBackupKeyEscrow) Reveal() (string, string, time.Time, error) {
	if f.escrowID == "" {
		return "", "", time.Time{}, ErrBackupKeyNotPending
	}
	return f.escrowID, fakeBackupKey, f.expiresAt, nil
}

func (f *fakeBackupKeyEscrow) Acknowledge(escrowID string) error {
	switch {
	case f.escrowID == "":
		return ErrBackupKeyNotPending
	case escrowID != f.escrowID:
		return ErrBackupKeyChanged
	}
	f.escrowID = ""
	return nil
}

// The backup key fields are Admin-Session gated, return the key only from
// revealBackupKey, refuse a stale escrow ID, and report nothing pending once
// the key is confirmed or when no escrow is wired.
func TestBackupKeyFieldsRevealAndAcknowledge(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)

	const (
		status = `{"query":"query { backupKeyStatus { pending expiresAt daysLeft } }"}`
		reveal = `{"query":"mutation { revealBackupKey { escrowID backupKey expiresAt daysLeft } }"}`
		ack    = `{"query":"mutation($id: String!) { acknowledgeBackupKey(escrowID: $id) { pending daysLeft } }","variables":{"id":%q}}`
	)
	for _, body := range []string{status, reveal, strings.Replace(ack, "%q", `"escrow-a"`, 1)} {
		if data, errs := postLibraryTrash(t, handler, body); len(errs) == 0 || errs[0] != "admin session required" || strings.Contains(string(data["revealBackupKey"]), fakeBackupKey) {
			t.Fatalf("%s without an Admin Session: data %s, errors %q", body, data, errs)
		}
	}
	openManagerAdminSession(t, st)

	var info BackupKeyStatusInfo
	data, errs := postLibraryTrash(t, handler, status)
	if len(errs) != 0 || json.Unmarshal(data["backupKeyStatus"], &info) != nil || info.Pending {
		t.Fatalf("backupKeyStatus with no escrow = %s, errors %q", data["backupKeyStatus"], errs)
	}
	if _, errs := postLibraryTrash(t, handler, reveal); len(errs) == 0 || errs[0] != ErrBackupKeyNotPending.Error() {
		t.Fatalf("revealBackupKey with no escrow: errors = %q", errs)
	}

	fake := &fakeBackupKeyEscrow{escrowID: "escrow-a", expiresAt: time.Now().Add(11*24*time.Hour + time.Hour)}
	handler.SetBackupKeyEscrow(fake)

	data, errs = postLibraryTrash(t, handler, status)
	info = BackupKeyStatusInfo{}
	if len(errs) != 0 || json.Unmarshal(data["backupKeyStatus"], &info) != nil || !info.Pending || info.DaysLeft != 12 || info.ExpiresAt == "" {
		t.Fatalf("backupKeyStatus pending = %s, errors %q", data["backupKeyStatus"], errs)
	}
	if strings.Contains(string(data["backupKeyStatus"]), fakeBackupKey) || strings.Contains(string(data["backupKeyStatus"]), "escrow-a") {
		t.Fatalf("backupKeyStatus leaked the key or escrow ID: %s", data["backupKeyStatus"])
	}

	var revealed RevealedBackupKeyInfo
	data, errs = postLibraryTrash(t, handler, reveal)
	if len(errs) != 0 || json.Unmarshal(data["revealBackupKey"], &revealed) != nil || revealed.EscrowID != "escrow-a" || revealed.BackupKey != fakeBackupKey || revealed.DaysLeft != 12 {
		t.Fatalf("revealBackupKey = %s, errors %q", data["revealBackupKey"], errs)
	}

	if _, errs := postLibraryTrash(t, handler, strings.Replace(ack, "%q", `"escrow-old"`, 1)); len(errs) == 0 || errs[0] != ErrBackupKeyChanged.Error() || fake.escrowID == "" {
		t.Fatalf("acknowledgeBackupKey with a stale escrow ID: errors = %q, pending %v", errs, fake.escrowID != "")
	}
	if _, errs := postLibraryTrash(t, handler, strings.Replace(ack, "%q", `" "`, 1)); len(errs) == 0 || errs[0] != "escrowID required" {
		t.Fatalf("acknowledgeBackupKey with a blank escrow ID: errors = %q", errs)
	}

	data, errs = postLibraryTrash(t, handler, strings.Replace(ack, "%q", `"escrow-a"`, 1))
	info = BackupKeyStatusInfo{}
	if len(errs) != 0 || json.Unmarshal(data["acknowledgeBackupKey"], &info) != nil || info.Pending || fake.escrowID != "" {
		t.Fatalf("acknowledgeBackupKey = %s, errors %q", data["acknowledgeBackupKey"], errs)
	}
	if _, errs := postLibraryTrash(t, handler, reveal); len(errs) == 0 || errs[0] != ErrBackupKeyNotPending.Error() {
		t.Fatalf("revealBackupKey after acknowledgement: errors = %q", errs)
	}
}

// No API token tier may reveal or confirm the backup key, even an agent token
// that runs every other Console field.
func TestBackupKeyRevealRefusesEveryTokenTier(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	handler.SetBackupKeyEscrow(&fakeBackupKeyEscrow{escrowID: "escrow-a", expiresAt: time.Now().Add(24 * time.Hour)})
	for _, access := range []store.APITokenAccess{store.APITokenAccessCLI, store.APITokenAccessDiagnostics, store.APITokenAccessAgent} {
		_, token, err := st.CreateAPITokenWithAccess(t.Context(), string(access), access, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		authorize := func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
		for _, body := range []string{
			`{"query":"mutation { revealBackupKey { backupKey } }"}`,
			`{"query":"mutation { acknowledgeBackupKey(escrowID: \"escrow-a\") { pending } }"}`,
		} {
			data, errs := post(t, handler, body, authorize)
			if len(errs) == 0 || errs[0].Message != "admin session required" || strings.Contains(string(data["revealBackupKey"]), fakeBackupKey) {
				t.Errorf("%s token: %s = %s, errors %#v", access, body, data, errs)
			}
		}
	}
}

func TestBackupKeyDaysLeftRoundsUp(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		left time.Duration
		want int
	}{{-time.Hour, 0}, {0, 0}, {time.Minute, 1}, {24 * time.Hour, 1}, {24*time.Hour + time.Second, 2}, {30 * 24 * time.Hour, 30}} {
		if got := backupKeyDaysLeft(now.Add(tc.left), now); got != tc.want {
			t.Errorf("days left for %s = %d, want %d", tc.left, got, tc.want)
		}
	}
}
