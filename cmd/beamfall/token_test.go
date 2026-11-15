package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/store"
)

// TestTokenCreateAccessTier (CORE-2095): `token create --access` mints the
// named tier offline and refuses an unknown tier before touching the store.
func TestTokenCreateAccessTier(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BEAMFALL_DATA_DIR", dir)
	ctx := context.Background()

	err := tokenCreate(ctx, []string{"--name", "x", "--access", "root"}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "token access must be") {
		t.Fatalf("--access root error = %v, want tier validation", err)
	}
	var out strings.Builder
	if err := tokenCreate(ctx, []string{"--name", "probe", "--access", "diagnostics"}, &out); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	verified, err := st.VerifyAPIToken(ctx, strings.TrimSpace(out.String()), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if verified.Access != store.APITokenAccessDiagnostics {
		t.Fatalf("access = %q, want diagnostics", verified.Access)
	}
}
