package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

func TestAPITokenCreateVerifyRoundTrip(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	created, secret, err := st.CreateAPIToken(ctx, "ci", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "bft_") {
		t.Fatalf("token %q lacks the bft_ prefix", secret)
	}
	if strings.Contains(secret, created.Name) {
		t.Fatal("token string must not embed the name")
	}

	verified, err := st.VerifyAPIToken(ctx, secret, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if verified.ID != created.ID {
		t.Fatalf("verified ID %q, want %q", verified.ID, created.ID)
	}
	if !verified.LastUsedAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("last used %s, want %s", verified.LastUsedAt, now.Add(time.Hour))
	}
}

func TestAPITokenVerifyRejectsBadTokens(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	_, secret, err := st.CreateAPIToken(ctx, "ci", now)
	if err != nil {
		t.Fatal(err)
	}

	wrongSecret := secret[:len(secret)-1] + map[bool]string{true: "0", false: "1"}[strings.HasSuffix(secret, "1")]
	bad := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no prefix", strings.TrimPrefix(secret, "bft_")},
		{"malformed", "bft_zz_zz"},
		{"unknown id", "bft_" + strings.Repeat("0", 16) + "_" + strings.Repeat("0", 64)},
		{"wrong secret", wrongSecret},
	}
	for _, tc := range bad {
		if _, err := st.VerifyAPIToken(ctx, tc.token, now); err == nil {
			t.Fatalf("%s: verify succeeded, want error", tc.name)
		} else if !strings.Contains(err.Error(), "invalid API token") {
			t.Fatalf("%s: error %q, want the generic invalid-token error", tc.name, err)
		}
	}
}

func TestAPITokenRevokeIsImmediateAndListed(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	created, secret, err := st.CreateAPIToken(ctx, "ci", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeAPIToken(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.VerifyAPIToken(ctx, secret, now); err == nil {
		t.Fatal("revoked token verified")
	} else if !strings.Contains(err.Error(), "invalid API token") {
		t.Fatalf("revoked verify error %q leaks revocation state", err)
	}
	tokens, err := st.APITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 || !tokens[0].Revoked {
		t.Fatalf("list = %+v, want one revoked row", tokens)
	}
	if err := st.RevokeAPIToken(ctx, "missing"); err == nil {
		t.Fatal("revoking an unknown token must fail")
	}
}

func TestAPITokenActivityAttribution(t *testing.T) {
	st := storetest.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	entry := store.ActivityEntry{
		OccurredAt: now,
		Action:     store.ActivityTokenCreate,
		TargetType: "token",
		TargetID:   "abc",
		TokenID:    "tok-1",
	}
	if err := st.AppendActivity(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entries, err := st.ActivityEntries(ctx, store.ActivityQuery{Filter: store.LibraryFilter{Revealed: true, AllLibraries: true}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 || entries[0].TokenID != "tok-1" {
		t.Fatalf("entries = %+v, want token attribution preserved", entries)
	}
}
