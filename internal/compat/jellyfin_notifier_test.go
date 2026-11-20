package compat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/beamfall/core/internal/auth"
	"github.com/beamfall/core/internal/store"
)

func scopedIntegrationToken(t *testing.T, service *auth.Service, scopes ...store.IntegrationScope) string {
	t.Helper()
	session, err := service.AuthenticateIntegrationByName(t.Context(), "Default", "2468", "127.0.0.1", scopes)
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

func TestJellyfinNotifierReadRoutes(t *testing.T) {
	_, service := integrationService(t, time.Now)
	handler := newIdentityHandler(t, Config{Enabled: true}, service)
	read := scopedIntegrationToken(t, service, store.IntegrationScopeRead)
	refreshOnly := scopedIntegrationToken(t, service, store.IntegrationScopeRefresh)
	const emptyList = `{"Items":[],"TotalRecordCount":0,"StartIndex":0}` + "\n"
	for _, tc := range []struct {
		method, path string
		status       int
		body         string
	}{
		{http.MethodGet, "System/Configuration", http.StatusOK, "{}\n"},
		{http.MethodGet, "Items?recursive=true&includeItemTypes=Movie&fields=Path,ProviderIds", http.StatusOK, emptyList},
		{http.MethodGet, "Library/MediaFolders", http.StatusOK, emptyList},
		{http.MethodGet, "mediabrowser/Library/MediaFolders", http.StatusOK, emptyList},
		{http.MethodPost, "Notifications/Admin", http.StatusNotFound, ""},
		{http.MethodPost, "mediabrowser/Notifications/Admin", http.StatusNotFound, ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := compatRequest(t, tc.method, tc.path)
			req.Body = io.NopCloser(strings.NewReader(`{"Name":"Test","Description":"x"}`))
			req.Header.Set("X-MediaBrowser-Token", read)
			rec := sendCompat(handler, req)
			if rec.Code != tc.status || (tc.body != "" && rec.Body.String() != tc.body) {
				t.Fatalf("authenticated response = %d %q, want %d %q", rec.Code, rec.Body, tc.status, tc.body)
			}
			for name, token := range map[string]string{"missing": "", "invalid": "not-a-token", "refresh only": refreshOnly} {
				req := compatRequest(t, tc.method, tc.path)
				if token != "" {
					req.Header.Set("X-MediaBrowser-Token", token)
				}
				rec := sendCompat(handler, req)
				if rec.Code != http.StatusUnauthorized || rec.Body.String() != "{\"error\":\"authentication failed\"}\n" {
					t.Fatalf("%s credential = %d %q, want uniform 401", name, rec.Code, rec.Body)
				}
			}
		})
	}
}

func TestJellyfinTokenHeaderPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"emby", map[string]string{"X-Emby-Token": "a", "X-MediaBrowser-Token": "b"}, "a"},
		{"mediabrowser", map[string]string{"X-MediaBrowser-Token": " b ", "Authorization": `MediaBrowser Token="c"`}, "b"},
		{"authorization", map[string]string{"X-MediaBrowser-Token": " ", "Authorization": `MediaBrowser Token="c"`}, "c"},
	} {
		header := http.Header{}
		for name, value := range tc.headers {
			header.Set(name, value)
		}
		if got := integrationToken(header); got != tc.want {
			t.Errorf("%s: token = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestJellyfinMediaUpdated(t *testing.T) {
	_, service := integrationService(t, time.Now)
	refresh := scopedIntegrationToken(t, service, store.IntegrationScopeRefresh)
	readOnly := scopedIntegrationToken(t, service, store.IntegrationScopeRead)
	tooMany := make([]string, mediaUpdateMaxPaths+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(`{"Path":"/media/%d","UpdateType":"Created"}`, i)
	}
	for _, tc := range []struct {
		name, path, token, body string
		status                  int
		want                    []string
	}{
		{"paths", "Library/Media/Updated", refresh,
			`{"Updates":[{"Path":"/media/movies/A (2020)","UpdateType":"Created"},{"Path":"","UpdateType":"Created"},{"Path":"/media/movies/B","UpdateType":"Modified"}]}`,
			http.StatusNoContent, []string{"/media/movies/A (2020)", "/media/movies/B"}},
		{"lidarr alias", "mediabrowser/Library/Media/Updated", refresh,
			`{"Updates":[{"Path":"/music/Artist","UpdateType":"Created"}]}`, http.StatusNoContent, []string{"/music/Artist"}},
		{"no paths", "Library/Media/Updated", refresh, `{"Updates":[]}`, http.StatusNoContent, nil},
		{"malformed", "Library/Media/Updated", refresh, `{"Updates":`, http.StatusBadRequest, nil},
		{"oversized body", "Library/Media/Updated", refresh,
			`{"Updates":[{"Path":"/` + strings.Repeat("a", mediaUpdateBodyLimit) + `"}]}`, http.StatusBadRequest, nil},
		{"too many paths", "Library/Media/Updated", refresh,
			`{"Updates":[` + strings.Join(tooMany, ",") + `]}`, http.StatusBadRequest, nil},
		{"read scope only", "Library/Media/Updated", readOnly,
			`{"Updates":[{"Path":"/media/movies/A","UpdateType":"Created"}]}`, http.StatusUnauthorized, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got [][]string
			handler := newIdentityHandler(t, Config{
				Enabled: true,
				EnqueueRefresh: func(_ context.Context, _ auth.Scope, paths []string) {
					got = append(got, paths)
				},
			}, service)
			req := compatRequest(t, http.MethodPost, tc.path)
			req.Body = io.NopCloser(strings.NewReader(tc.body))
			req.Header.Set("X-MediaBrowser-Token", tc.token)
			rec := sendCompat(handler, req)
			if rec.Code != tc.status {
				t.Fatalf("media update returned %d, want %d", rec.Code, tc.status)
			}
			if tc.status == http.StatusNoContent && rec.Body.Len() != 0 {
				t.Fatalf("media update returned %d body bytes, want none", rec.Body.Len())
			}
			switch {
			case tc.want == nil && len(got) != 0:
				t.Fatalf("media update enqueued %v, want nothing", got)
			case tc.want != nil && (len(got) != 1 || !slices.Equal(got[0], tc.want)):
				t.Fatalf("media update enqueued %v, want one call with %v", got, tc.want)
			}
		})
	}
}

func TestJellyfinMediaUpdatedSharesRefreshAdmission(t *testing.T) {
	_, service := integrationService(t, time.Now)
	token := scopedIntegrationToken(t, service, store.IntegrationScopeRefresh)
	var calls [][]string
	handler := newIdentityHandler(t, Config{
		Enabled:        true,
		EnqueueRefresh: func(_ context.Context, _ auth.Scope, paths []string) { calls = append(calls, paths) },
	}, service)
	for _, path := range []string{"Library/Refresh", "Library/Media/Updated", "mediabrowser/Library/Refresh"} {
		req := compatRequest(t, http.MethodPost, path)
		req.Body = io.NopCloser(strings.NewReader(`{"Updates":[{"Path":"/media/a","UpdateType":"Created"}]}`))
		req.Header.Set("X-MediaBrowser-Token", token)
		if rec := sendCompat(handler, req); rec.Code != http.StatusNoContent {
			t.Fatalf("%s returned %d, want 204", path, rec.Code)
		}
	}
	if len(calls) != 1 || calls[0] != nil {
		t.Fatalf("enqueue calls = %v, want one full refresh", calls)
	}
}
