package offlinepack

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGrantProtocolValidationMatrix(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	authority := testAuthority(t, now)
	claims := testGrantClaims(now)
	token, err := authority.SignGrant(claims)
	if err != nil {
		t.Fatal(err)
	}
	expected := expectationsFor(claims, now)

	got, replay, decision, err := VerifyGrant(token, authority.PublicKeys(), expected)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != claims.Seq || replay.HighestSeq != claims.Seq || decision.Destroy || decision.Refresh {
		t.Fatalf("valid decision = %+v replay=%+v", decision, replay)
	}

	parts := strings.Split(token, ".")
	var header map[string]json.RawMessage
	var payload map[string]json.RawMessage
	mustDecodeSegment(t, parts[0], &header)
	mustDecodeSegment(t, parts[1], &payload)
	if len(header) != len(headerKeys) || len(payload) != len(grantClaimKeys) {
		t.Fatalf("header/claim cardinality = %d/%d", len(header), len(payload))
	}

	t.Run("artifact type confusion", func(t *testing.T) {
		var manifest ManifestClaims
		if _, err := verifyCompact(token, ManifestType, authority.PublicKeys(), &manifest, manifestClaimKeys); !errors.Is(err, ErrArtifactType) {
			t.Fatalf("error = %v", err)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong alg", func(h map[string]any) { h["alg"] = "HS256" }},
		{"wrong typ", func(h map[string]any) { h["typ"] = StatusType }},
		{"missing crit", func(h map[string]any) { delete(h, "crit") }},
		{"unknown protected member", func(h map[string]any) { h["extra"] = true }},
		{"unknown kid", func(h map[string]any) { h["kid"] = "unknown" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := map[string]any{"alg": "EdDSA", "typ": GrantType, "bfgv": 1, "crit": []string{"bfgv"}, "kid": authority.kid}
			tc.mutate(h)
			bad := signRawTest(t, authority, h, claims)
			if _, _, _, err := VerifyGrant(bad, authority.PublicKeys(), expected); err == nil {
				t.Fatal("accepted invalid protected header")
			}
		})
	}

	t.Run("unknown claim", func(t *testing.T) {
		var p map[string]any
		mustDecodeSegment(t, parts[1], &p)
		p["extra"] = true
		h := map[string]any{"alg": "EdDSA", "typ": GrantType, "bfgv": 1, "crit": []string{"bfgv"}, "kid": authority.kid}
		bad := signRawTest(t, authority, h, p)
		if _, _, _, err := VerifyGrant(bad, authority.PublicKeys(), expected); err == nil {
			t.Fatal("accepted unknown claim")
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"wrong audience", func(p map[string]any) { p["aud"] = "other" }},
		{"wrong version", func(p map[string]any) { p["v"] = 2 }},
		{"lifetime over maximum", func(p map[string]any) { p["exp"] = float64(claims.Iat) + GrantLifetime.Seconds() + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p map[string]any
			mustDecodeSegment(t, parts[1], &p)
			tc.mutate(p)
			h := map[string]any{"alg": "EdDSA", "typ": GrantType, "bfgv": 1, "crit": []string{"bfgv"}, "kid": authority.kid}
			bad := signRawTest(t, authority, h, p)
			if _, _, _, err := VerifyGrant(bad, authority.PublicKeys(), expected); err == nil {
				t.Fatal("accepted invalid grant claim")
			}
		})
	}

	t.Run("signature tamper", func(t *testing.T) {
		bad := token[:len(token)-1] + flipBase64url(token[len(token)-1:])
		if _, _, _, err := VerifyGrant(bad, authority.PublicKeys(), expected); err == nil {
			t.Fatal("accepted tampered signature")
		}
	})
}

func TestAuthenticatedGrantVerifiesOnceAndFailsClosed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	authority := testAuthority(t, now)
	claims := testGrantClaims(now)
	token, err := authority.SignGrant(claims)
	if err != nil {
		t.Fatal(err)
	}
	validations := 0
	authority.verify = func(key ed25519.PublicKey, message, signature []byte) bool {
		validations++
		return ed25519.Verify(key, message, signature)
	}
	authenticated, err := authority.AuthenticateGrant(token)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := authority.VerifyAuthenticatedGrant(authenticated, expectationsFor(claims, now)); err != nil {
		t.Fatal(err)
	}
	exposed := authenticated.Grant()
	exposed.LibraryIDs[0] = "fabricated"
	if _, _, _, err := authority.VerifyAuthenticatedGrant(authenticated, expectationsFor(claims, now)); err != nil {
		t.Fatalf("mutating exposed grant changed opaque proof: %v", err)
	}
	if validations != 1 {
		t.Fatalf("signature validations = %d, want 1", validations)
	}
	if _, _, _, err := authority.VerifyAuthenticatedGrant(AuthenticatedGrant{}, expectationsFor(claims, now)); err == nil {
		t.Fatal("zero authenticated grant accepted")
	}
	other := testAuthority(t, now)
	if _, _, _, err := other.VerifyAuthenticatedGrant(authenticated, expectationsFor(claims, now)); err == nil {
		t.Fatal("cross-authority authenticated grant accepted")
	}
}

func TestGrantBindingsExpiryReplayAndRevocations(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	authority := testAuthority(t, now)
	base := testGrantClaims(now)

	for _, tc := range []struct {
		name   string
		mutate func(*GrantExpectations)
		want   error
	}{
		{"pack", func(e *GrantExpectations) { e.PackID = "other" }, nil},
		{"profile", func(e *GrantExpectations) { e.ProfileID = "other" }, nil},
		{"library", func(e *GrantExpectations) { e.LibraryIDs = []string{"other"} }, nil},
		{"device enrollment", func(e *GrantExpectations) { e.DeviceEnrollmentID = "other" }, nil},
		{"device key", func(e *GrantExpectations) { e.DeviceKeyThumbprint = digestText("other") }, nil},
		{"manifest revision", func(e *GrantExpectations) { e.ManifestRevision++ }, nil},
		{"manifest digest", func(e *GrantExpectations) { e.ManifestSHA256 = digestText("other") }, nil},
	} {
		t.Run(tc.name+" binding", func(t *testing.T) {
			token, err := authority.SignGrant(base)
			if err != nil {
				t.Fatal(err)
			}
			expected := expectationsFor(base, now)
			tc.mutate(&expected)
			if _, _, decision, err := VerifyGrant(token, authority.PublicKeys(), expected); err == nil || decision.Destroy {
				t.Fatalf("error/decision = %v/%+v", err, decision)
			}
		})
	}

	t.Run("expiry no grace", func(t *testing.T) {
		for _, delta := range []time.Duration{-time.Second, 0} {
			claims := base
			claims.Exp = now.Add(delta).Unix()
			claims.Iat = now.Add(-GrantLifetime).Unix()
			token, err := authority.SignGrant(claims)
			if err != nil {
				t.Fatal(err)
			}
			_, _, decision, err := VerifyGrant(token, authority.PublicKeys(), expectationsFor(claims, now))
			if !errors.Is(err, ErrGrantExpired) || !decision.Destroy {
				t.Fatalf("delta=%v error/decision = %v/%+v", delta, err, decision)
			}
		}
	})

	t.Run("issuance skew boundary", func(t *testing.T) {
		for _, tc := range []struct {
			offset time.Duration
			ok     bool
		}{{IssuanceSkew, true}, {IssuanceSkew + time.Second, false}} {
			claims := base
			claims.Iat = now.Add(tc.offset).Unix()
			claims.Nbf = claims.Iat
			claims.Exp = claims.Iat + int64(GrantLifetime/time.Second)
			token, err := authority.SignGrant(claims)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, verifyErr := VerifyGrant(token, authority.PublicKeys(), expectationsFor(claims, now))
			if (verifyErr == nil) != tc.ok {
				t.Fatalf("offset=%v error=%v", tc.offset, verifyErr)
			}
		}
	})

	t.Run("sequence replay", func(t *testing.T) {
		token, err := authority.SignGrant(base)
		if err != nil {
			t.Fatal(err)
		}
		expected := expectationsFor(base, now)
		_, accepted, _, err := VerifyGrant(token, authority.PublicKeys(), expected)
		if err != nil {
			t.Fatal(err)
		}
		expected.Previous = accepted
		if _, _, _, err := VerifyGrant(token, authority.PublicKeys(), expected); err != nil {
			t.Fatalf("identical retry: %v", err)
		}
		older := base
		older.Seq--
		olderToken, err := authority.SignGrant(older)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := VerifyGrant(olderToken, authority.PublicKeys(), expected); !errors.Is(err, ErrGrantReplay) {
			t.Fatalf("rollback error = %v", err)
		}
		conflict := base
		conflict.JTI = base64.RawURLEncoding.EncodeToString([]byte("fedcba9876543210"))
		conflictToken, err := authority.SignGrant(conflict)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := VerifyGrant(conflictToken, authority.PublicKeys(), expected); !errors.Is(err, ErrGrantReplay) {
			t.Fatalf("equal-sequence conflict error = %v", err)
		}
	})

	for _, generation := range []struct {
		name   string
		mutate func(*RevocationGenerations)
	}{
		{"grant", func(r *RevocationGenerations) { r.Grant++ }},
		{"enrollment", func(r *RevocationGenerations) { r.Enrollment++ }},
		{"profile", func(r *RevocationGenerations) { r.Profile++ }},
		{"library policy", func(r *RevocationGenerations) { r.LibraryPolicy++ }},
		{"backup", func(r *RevocationGenerations) { r.Backup++ }},
	} {
		t.Run(generation.name+" generation destroys", func(t *testing.T) {
			token, err := authority.SignGrant(base)
			if err != nil {
				t.Fatal(err)
			}
			expected := expectationsFor(base, now)
			generation.mutate(&expected.Rev)
			_, _, decision, err := VerifyGrant(token, authority.PublicKeys(), expected)
			if !errors.Is(err, ErrGeneration) || !decision.Destroy {
				t.Fatalf("error/decision = %v/%+v", err, decision)
			}
		})
	}
}

func TestTrustedTimeRollbackResistance(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	for _, tc := range []struct {
		name string
		in   TrustedTimeInput
		want time.Time
		err  error
	}{
		{"same boot advances from authenticated sync", TrustedTimeInput{base.Add(-time.Hour), base, 2 * time.Hour, base.Add(time.Hour), true}, base.Add(2 * time.Hour), nil},
		{"same boot keeps high water", TrustedTimeInput{base, base, time.Minute, base.Add(3 * time.Hour), true}, base.Add(3 * time.Hour), nil},
		{"reboot tolerates bounded rollback", TrustedTimeInput{WallClock: base, HighWater: base.Add(IssuanceSkew), SameGeneration: false}, base.Add(IssuanceSkew), nil},
		{"reboot rejects rollback", TrustedTimeInput{WallClock: base, HighWater: base.Add(IssuanceSkew + time.Second), SameGeneration: false}, time.Time{}, ErrNeedsAuthSync},
		{"missing authenticated sync", TrustedTimeInput{WallClock: base, HighWater: base, SameGeneration: true}, time.Time{}, ErrNeedsAuthSync},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TrustedNow(tc.in)
			if !errors.Is(err, tc.err) || !got.Equal(tc.want) {
				t.Fatalf("TrustedNow = %v, %v", got, err)
			}
		})
	}
}

func TestAuthorityIssueBoundaryAndRefreshWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	authority := testAuthority(t, now)
	claims := testGrantClaims(now)
	claims.Exp = now.Add(RefreshWindow).Unix()
	token, err := authority.SignGrant(claims)
	if err != nil {
		t.Fatal(err)
	}
	_, _, decision, err := VerifyGrant(token, authority.PublicKeys(), expectationsFor(claims, now))
	if err != nil || !decision.Refresh {
		t.Fatalf("refresh decision = %+v, %v", decision, err)
	}
	authority.SetIssueEnabled(false)
	if _, err := authority.SignGrant(claims); err == nil {
		t.Fatal("non-authority role issued a grant")
	}
}

func testAuthority(t *testing.T, now time.Time) *Authority {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewAuthority("spki-sha256(test)", "test-key", privateKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func testGrantClaims(now time.Time) GrantClaims {
	return GrantClaims{
		V: 1, Iss: "spki-sha256(test)", Aud: OfflinePackAudience,
		JTI: base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
		Seq: 2, PackID: "pack-1", ProfileID: "profile-1", LibraryIDs: []string{"library-1"},
		DeviceEnrollmentID: "device-1", DeviceKeyThumbprint: digestText("device"),
		ManifestRevision: 1, ManifestSHA256: digestText("manifest"), Rights: []string{OfflinePlayRight},
		Iat: now.Unix(), Nbf: now.Unix(), Exp: now.Add(GrantLifetime).Unix(),
		Rev: RevocationGenerations{Grant: 1, Enrollment: 1, Profile: 1, LibraryPolicy: 1, Backup: 1},
	}
}

func expectationsFor(c GrantClaims, now time.Time) GrantExpectations {
	return GrantExpectations{
		Issuer: c.Iss, PackID: c.PackID, ProfileID: c.ProfileID, LibraryIDs: c.LibraryIDs,
		DeviceEnrollmentID: c.DeviceEnrollmentID, DeviceKeyThumbprint: c.DeviceKeyThumbprint,
		ManifestRevision: c.ManifestRevision, ManifestSHA256: c.ManifestSHA256, Rev: c.Rev, TrustedNow: now,
	}
}

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func mustDecodeSegment(t *testing.T, segment string, dst any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatal(err)
	}
}

func signRawTest(t *testing.T, authority *Authority, header, payload any) string {
	t.Helper()
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	input := rawURL(headerJSON) + "." + rawURL(payloadJSON)
	return input + "." + rawURL(ed25519.Sign(authority.privateKey, []byte(input)))
}

func flipBase64url(value string) string {
	if value == "A" {
		return "B"
	}
	return "A"
}
