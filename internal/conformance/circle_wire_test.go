package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/beamfall/core/internal/mediahttp"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/streamtoken"
)

type circleCompatibilityRow struct {
	OwnerCurrent int    `json:"ownerCurrent"`
	GuestCurrent int    `json:"guestCurrent"`
	Serve        bool   `json:"serve"`
	Revision     int    `json:"revision"`
	CloseReason  string `json:"closeReason"`
}

type circleWireArtifact struct {
	Boundary            string                   `json:"boundary"`
	CurrentRevision     int                      `json:"currentRevision"`
	PreviousRevision    int                      `json:"previousRevision"`
	SupportedRevisions  []int                    `json:"supportedRevisions"`
	FrameFields         []string                 `json:"frameFields"`
	Operations          []string                 `json:"operations"`
	CompatibilityMatrix []circleCompatibilityRow `json:"compatibilityMatrix"`
}

func TestCircleWireArtifactDrivesProductionNegotiation(t *testing.T) {
	body, err := os.ReadFile("../../contracts/circle-wire/circle-wire.json")
	if err != nil {
		t.Fatal(err)
	}
	var artifact circleWireArtifact
	if err := json.Unmarshal(body, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Boundary != "circle-federation-wire" || artifact.CurrentRevision != streamtoken.CurrentRevision || artifact.PreviousRevision != streamtoken.PreviousRevision {
		t.Fatalf("artifact revision drift: %+v", artifact)
	}
	if !slices.Equal(artifact.SupportedRevisions, []int{streamtoken.CurrentRevision, streamtoken.PreviousRevision}) {
		t.Fatalf("support window = %v", artifact.SupportedRevisions)
	}
	if len(artifact.CompatibilityMatrix) != 5 {
		t.Fatalf("matrix rows = %d", len(artifact.CompatibilityMatrix))
	}
	for _, row := range artifact.CompatibilityMatrix {
		got := streamtoken.Negotiate(row.OwnerCurrent, row.GuestCurrent)
		if got.Serve != row.Serve || got.Revision != row.Revision || string(got.CloseReason) != row.CloseReason {
			t.Fatalf("matrix row %+v => %+v", row, got)
		}
	}
	if !slices.Equal(artifact.FrameFields, []string{"revision", "circleSessionId", "operation", "kind", "requestId", "payload"}) {
		t.Fatalf("frame fields = %v", artifact.FrameFields)
	}
}

// TestCircleWireTier2CompatibilityMatrix is ADR-0215's blocking Tier-2
// cross-repo contract suite. Tier 3 remains a nightly-first follow-up: run these
// same five committed rows between two independently built real Core binaries.
func TestCircleWireTier2CompatibilityMatrix(t *testing.T) {
	body, err := os.ReadFile("../../contracts/circle-wire/vectors/2/compatibility-matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []circleCompatibilityRow
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("Tier-2 matrix rows = %d, want 5", len(rows))
	}
	retirementTested := false
	for _, row := range rows {
		row := row
		t.Run(fmt.Sprintf("owner-%d-guest-%d", row.OwnerCurrent, row.GuestCurrent), func(t *testing.T) {
			owner := &inProcessCircleCore{currentRevision: row.OwnerCurrent}
			guest := &inProcessCircleCore{currentRevision: row.GuestCurrent}
			assertCircleSupportWindow(t, owner)
			assertCircleSupportWindow(t, guest)

			got := connectCircleCores(t, owner, guest)
			if got.Serve != row.Serve || got.Revision != row.Revision || string(got.CloseReason) != row.CloseReason {
				t.Fatalf("matrix row %+v => %+v", row, got)
			}
			if !row.Serve {
				if owner.session != nil || guest.session != nil {
					t.Fatal("unsupported revisions opened a session")
				}
				if err := owner.accept(circleCatalogFrame(0, streamtoken.KindRequest)); !errors.Is(err, streamtoken.ErrRevisionUnsupported) {
					t.Fatalf("owner served after negotiation refusal: %v", err)
				}
				if err := guest.accept(circleCatalogFrame(0, streamtoken.KindResponse)); !errors.Is(err, streamtoken.ErrRevisionUnsupported) {
					t.Fatalf("guest served after negotiation refusal: %v", err)
				}
				if owner.dispatched != 0 || guest.dispatched != 0 {
					t.Fatalf("refused pair dispatched owner=%d guest=%d", owner.dispatched, guest.dispatched)
				}
				if row.OwnerCurrent == streamtoken.CurrentRevision+1 && row.GuestCurrent == streamtoken.PreviousRevision {
					retirementTested = true
					if slices.Contains(owner.supported, streamtoken.PreviousRevision) {
						t.Fatalf("atomic N+1 publication retained N-1: owner supports %v", owner.supported)
					}
				}
				return
			}

			if owner.session.Revision() != row.Revision || guest.session.Revision() != row.Revision {
				t.Fatalf("sessions did not pin revision %d: owner=%d guest=%d", row.Revision, owner.session.Revision(), guest.session.Revision())
			}
			if err := owner.accept(circleCatalogFrame(row.Revision, streamtoken.KindRequest)); err != nil {
				t.Fatalf("owner rejected negotiated request: %v", err)
			}
			if err := guest.accept(circleCatalogFrame(row.Revision, streamtoken.KindResponse)); err != nil {
				t.Fatalf("guest rejected negotiated response: %v", err)
			}
			mismatch := circleCatalogFrame(row.Revision+1, streamtoken.KindRequest)
			if err := owner.accept(mismatch); !errors.Is(err, streamtoken.ErrRevisionMismatch) {
				t.Fatalf("owner mismatch refusal = %v", err)
			}
			mismatch.Kind = streamtoken.KindResponse
			if err := guest.accept(mismatch); !errors.Is(err, streamtoken.ErrRevisionMismatch) {
				t.Fatalf("guest mismatch refusal = %v", err)
			}
			if owner.dispatched != 1 || guest.dispatched != 1 {
				t.Fatalf("mismatched operation dispatched owner=%d guest=%d", owner.dispatched, guest.dispatched)
			}
		})
	}
	if !retirementTested {
		t.Fatal("Tier-2 matrix omitted atomic N+1 publication/N-1 retirement row")
	}
}

func TestCircleRevocationTerminatesActiveFederatedStreamWithPeerUnreachable(t *testing.T) {
	ctx := context.Background()
	ownerState, err := store.Open(ctx, t.TempDir(), store.Options{SeedDevelopmentData: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ownerState.Close()
	if err := ownerState.SetProfileOwner(ctx, "profile-default"); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerState.CreateCircle(ctx, "circle-stream"); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerState.BindShadowPrincipal(ctx, store.BindShadowPrincipalInput{
		CircleID: "circle-stream", RemoteProfileID: "remote-stream", RemoteProfileBinding: "binding-stream",
		PolicyProfileID: "profile-default", ActorProfileID: "profile-default", Revealed: true,
	}); err != nil {
		t.Fatal(err)
	}

	ownerCore := &inProcessCircleCore{currentRevision: streamtoken.CurrentRevision}
	guestCore := &inProcessCircleCore{currentRevision: streamtoken.CurrentRevision}
	negotiation := connectCircleCores(t, ownerCore, guestCore)
	now := time.Now().UTC()
	tokens := streamtoken.NewService([]byte("circle-stream-test-key"), func() time.Time { return now })
	tokens.SetCircleLeaseResolver(ownerState)
	circles := streamtoken.NewCircleService(ownerState, tokens, func() time.Time { return now }, nil)
	if _, err := circles.OpenSession(ctx, "circle-stream", "remote-stream", "stream-session", negotiation.Revision, "guest-spki"); err != nil {
		t.Fatal(err)
	}
	authorization, err := circles.Authorize(ctx, streamtoken.AuthorizationRequest{
		CircleID: "circle-stream", CircleSessionID: "stream-session", RemoteProfileID: "remote-stream",
		Operation: streamtoken.OperationPlaybackAuthorize, LeaseScope: streamtoken.CircleScopeMediaStream,
		NegotiatedRevision: negotiation.Revision, GuestServerSPKIPin: "guest-spki", MediaID: "media-alpha",
	})
	if err != nil {
		t.Fatal(err)
	}

	const mediaSize = 32 << 20
	mediaPath := filepath.Join(t.TempDir(), "federated-media.bin")
	mediaFile, err := os.OpenFile(mediaPath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- the test creates this file under its own t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := mediaFile.Truncate(mediaSize); err != nil {
		_ = mediaFile.Close()
		t.Fatal(err)
	}
	if err := mediaFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ownerState.SetPrimaryMediaFile(ctx, "media-alpha", mediaPath); err != nil {
		t.Fatal(err)
	}

	media := mediahttp.NewHandler(ownerState, nil, tokens, nil, nil)
	streamEnded := make(chan struct{})
	ownerHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		media.Direct(w, r)
		close(streamEnded)
	}))
	defer ownerHTTP.Close()
	response, err := ownerHTTP.Client().Get(ownerHTTP.URL + "/api/media/direct/" + authorization.Token)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("federated stream status = %d: %s", response.StatusCode, body)
	}
	first := make([]byte, 1)
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatalf("start federated byte flow: %v", err)
	}
	select {
	case <-streamEnded:
		t.Fatal("federated stream completed before revocation")
	default:
	}

	// The guest now stops consuming bytes, so the owner cannot depend on peer
	// cooperation or a courtesy revoke notice to release the blocked stream.
	revokedAt := time.Now()
	if _, err := ownerState.RevokeCircle(ctx, "circle-stream", "profile-default", revokedAt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-streamEnded:
		if elapsed := time.Since(revokedAt); elapsed >= streamtoken.CircleLeaseTTL {
			t.Fatalf("active federated stream ended after %v, lease TTL %v", elapsed, streamtoken.CircleLeaseTTL)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("active federated stream survived local revocation while peer was unreachable")
	}
	flowEnded := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(io.Discard, response.Body)
		flowEnded <- n
	}()
	select {
	case remaining := <-flowEnded:
		if remaining+int64(len(first)) >= mediaSize {
			t.Fatal("revoked federated stream delivered the complete Media body")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revoked federated byte flow did not terminate at the guest")
	}
	if _, err := ownerState.ShadowPrincipal(ctx, "circle-stream", "remote-stream"); !errors.Is(err, store.ErrShadowPrincipalNotFound) {
		t.Fatalf("revocation retained shadow principal: %v", err)
	}
	if _, err := ownerState.CircleSession(ctx, "circle-stream", "stream-session"); !errors.Is(err, store.ErrCircleSessionNotFound) {
		t.Fatalf("revocation retained cached session: %v", err)
	}
}

type inProcessCircleCore struct {
	currentRevision int
	supported       []int
	session         *streamtoken.Session
	dispatched      int
}

func assertCircleSupportWindow(t *testing.T, core *inProcessCircleCore) {
	t.Helper()
	var err error
	core.supported, err = streamtoken.SupportedRevisions(core.currentRevision)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{core.currentRevision, core.currentRevision - 1}; !slices.Equal(core.supported, want) {
		t.Fatalf("revision %d support window = %v, want %v", core.currentRevision, core.supported, want)
	}
}

func connectCircleCores(t *testing.T, owner, guest *inProcessCircleCore) streamtoken.Negotiation {
	t.Helper()
	negotiation := streamtoken.Negotiate(owner.currentRevision, guest.currentRevision)
	if !negotiation.Serve {
		return negotiation
	}
	var err error
	owner.session, err = streamtoken.NewSession("tier-2-session", negotiation.Revision)
	if err != nil {
		t.Fatal(err)
	}
	guest.session, err = streamtoken.NewSession("tier-2-session", negotiation.Revision)
	if err != nil {
		t.Fatal(err)
	}
	return negotiation
}

func (core *inProcessCircleCore) accept(frame streamtoken.Frame) error {
	if core.session == nil {
		return streamtoken.ErrRevisionUnsupported
	}
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return core.session.Accept(body, func(streamtoken.Frame) error {
		core.dispatched++
		return nil
	})
}

func circleCatalogFrame(revision int, kind streamtoken.Kind) streamtoken.Frame {
	return streamtoken.Frame{
		Revision: revision, CircleSessionID: "tier-2-session", Operation: streamtoken.OperationCatalogRead,
		Kind: kind, RequestID: "tier-2-request", Payload: json.RawMessage(`{"circleId":"circle-1","remoteProfileId":"remote-1"}`),
	}
}

func TestCircleWireRegistryEntry(t *testing.T) {
	body, err := os.ReadFile("../../contracts/registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Boundaries []struct {
			Boundary, Artifact string
			KnownDrift         []any
		} `json:"boundaries"`
	}
	if err := json.Unmarshal(body, &registry); err != nil {
		t.Fatal(err)
	}
	for _, boundary := range registry.Boundaries {
		if boundary.Boundary == "circle-federation-wire" {
			if boundary.Artifact != "contracts/circle-wire/circle-wire.json" || len(boundary.KnownDrift) != 0 {
				t.Fatalf("registry row = %+v", boundary)
			}
			return
		}
	}
	t.Fatal("circle-federation-wire registry row missing")
}
