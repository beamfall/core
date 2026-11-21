package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/beamfall/core/internal/l10n"
	"github.com/beamfall/core/internal/store"
	"github.com/beamfall/core/internal/store/storetest"
)

// CORE-2102: the folder-check refusal is rendered in the request locale, and
// its code stays INVALID.
func TestLibraryRootRefusalIsLocalized(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	openManagerAdminSession(t, st)
	missing := filepath.Join(t.TempDir(), "missing")
	quoted := strings.ReplaceAll(missing, `\`, `\\`)
	body := `{"query":"mutation($title: String!, $roots: [String!]) { createLibrary(title: $title, roots: $roots) { id } }","variables":{"title":"Films","roots":["` + quoted + `"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(body)))
	req = req.WithContext(l10n.WithLocale(req.Context(), "de"))
	authorizeManager(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp struct {
		Errors []graphError `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := "Den Ordner " + missing + " gibt es auf dem Server nicht."
	if len(resp.Errors) != 1 || resp.Errors[0].Message != want {
		t.Fatalf("errors = %#v, want message %q", resp.Errors, want)
	}
	if code := resp.Errors[0].Extensions.Code; code != ErrorCodeInvalid {
		t.Fatalf("code = %v, want %s", code, ErrorCodeInvalid)
	}
}

// The store's English refusal text and the source catalog must say the same
// thing, since logs and the source locale use the former.
func TestLibraryRootRefusalTextMatchesSourceCatalog(t *testing.T) {
	for reason, key := range libraryRootKeys {
		err := localizeLibraryRootError(&store.LibraryRootError{Reason: reason, Path: "/media/x"})
		var le *l10n.Error
		if !errors.As(err, &le) {
			t.Fatalf("%s: not localized", reason)
		}
		if got, want := le.Localize(l10n.Source()), err.Error(); got != want {
			t.Errorf("%s (%s): catalog %q, store %q", reason, key, got, want)
		}
	}
}

// CORE-2102: inbox titles render in the request locale from the kind; a kind
// with no catalog entry keeps its stored title.
func TestNotificationTitlesAreLocalized(t *testing.T) {
	st := storetest.Open(t)
	handler := newTestHandlerWithStore(st)
	ctx := context.Background()
	for _, n := range []store.Notification{
		{ID: "l10n-invite", Kind: store.NotificationWatchPartyInvite, Title: storedNotificationTitle(store.NotificationWatchPartyInvite)},
		{ID: "l10n-seek", Kind: store.NotificationWatchPartyRequest, Title: storedNotificationTitle(store.NotificationWatchPartyRequest), Body: legacyParticipantFallback},
		{ID: "l10n-system", Kind: store.NotificationSystem, Title: "Server note"},
	} {
		n.RecipientProfileID = "profile-default"
		if err := st.CreateNotification(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/graphql", bytes.NewReader([]byte(`{"query":"{ notifications { id title body } }"}`)))
	req = req.WithContext(l10n.WithLocale(req.Context(), "fr"))
	authorizeViewer(req)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var resp notificationsResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	got := map[string]NotificationInfo{}
	for _, n := range resp.Data.Notifications {
		got[n.ID] = n
	}
	if got["l10n-invite"].Title != "Invitation Watch Party" {
		t.Errorf("invite title = %q", got["l10n-invite"].Title)
	}
	if got["l10n-seek"].Title != "Demande de déplacement Watch Party" || got["l10n-seek"].Body != "Participant" {
		t.Errorf("seek = %+v", got["l10n-seek"])
	}
	if got["l10n-system"].Title != "Server note" {
		t.Errorf("system title = %q", got["l10n-system"].Title)
	}
}

func TestEveryCreatedNotificationKindHasATitle(t *testing.T) {
	for _, kind := range []store.NotificationKind{
		store.NotificationAccessRequest,
		store.NotificationExtensionRequest,
		store.NotificationWatchPartyInvite,
		store.NotificationWatchPartyRequest,
	} {
		if _, ok := notificationTitleKeys[kind]; !ok {
			t.Errorf("notification kind %q has no title key", kind)
		}
	}
}

func TestSearchFeedsThrottleIsLocalized(t *testing.T) {
	handler := newTestHandler(t)
	handler.SetFeedSearcher(feedSearchFunc(func(context.Context, string) ([]FeedSearchResult, error) {
		return nil, ErrFeedSearchThrottled
	}))
	body := `{"query":"{ searchFeeds(query:\"daily\") { feedURL } }"}`
	for locale, want := range map[string]string{
		"en": "Too many feed searches. Try again shortly.",
		"de": "Zu viele Feed-Suchen. Versuche es in Kürze erneut.",
		"es": "Demasiadas búsquedas de feeds. Inténtalo de nuevo en breve.",
		"fr": "Trop de recherches de flux. Réessayez dans un instant.",
		"pt": "Demasiadas pesquisas de feeds. Tente novamente dentro de instantes.",
	} {
		t.Run(locale, func(t *testing.T) {
			_, errs := post(t, handler, body, func(req *http.Request) {
				authorizeViewer(req)
				*req = *req.WithContext(l10n.WithLocale(req.Context(), locale))
			})
			if len(errs) != 1 || errs[0].Message != want || errs[0].Extensions.Code != ErrorCodeThrottled {
				t.Fatalf("errors = %+v, want THROTTLED with %q", errs, want)
			}
		})
	}
}

func TestSearchFeedsInvalidURLIsLocalized(t *testing.T) {
	handler := newTestHandler(t)
	handler.SetFeedSearcher(feedSearchFunc(func(context.Context, string) ([]FeedSearchResult, error) {
		return nil, ErrFeedSearchInvalidURL
	}))
	body := `{"query":"{ searchFeeds(query:\"https:/example.org/feed?token=x\") { feedURL } }"}`
	for locale, want := range map[string]string{
		"en": "Enter a valid website URL.",
		"de": "Gib eine gültige Website-URL ein.",
		"es": "Introduce una URL de sitio web válida.",
		"fr": "Saisissez une URL de site web valide.",
		"pt": "Introduza um URL de site válido.",
	} {
		t.Run(locale, func(t *testing.T) {
			_, errs := post(t, handler, body, func(req *http.Request) {
				authorizeViewer(req)
				*req = *req.WithContext(l10n.WithLocale(req.Context(), locale))
			})
			if len(errs) != 1 || errs[0].Message != want || errs[0].Extensions.Code != ErrorCodeInvalid {
				t.Fatalf("errors = %+v, want INVALID with %q", errs, want)
			}
		})
	}
}
