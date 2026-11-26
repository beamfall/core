package store

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestHomeConfigCRUDAndValidation(t *testing.T) {
	st := openSeeded(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Empty on fresh store.
	if _, ok, err := st.HomeConfig(ctx, HomeConfigScopeProfile, "profile-default"); err != nil || ok {
		t.Fatalf("expected no config, got ok=%v err=%v", ok, err)
	}

	cfg := HomeConfig{
		Version: 1,
		Hero:    HomeHeroConfig{Enabled: true, Mode: "auto"},
		Shelves: []HomeShelf{
			{
				Title:         "Unwatched Gems",
				HideWhenEmpty: true,
				Display:       HomeShelfDisplay{Style: "shelf", ArtworkSlot: "poster", Limit: 16},
				Source: HomeShelfSource{
					Type: "rules",
					Rules: &SmartRuleGroup{
						Match: "all",
						Conditions: []SmartRuleNode{
							{Field: "watched", Op: "isFalse"},
							{Field: "rating", Op: "gte", Value: 4},
						},
					},
					Sort: &HomeShelfSort{By: "added", Desc: true},
				},
			},
		},
	}
	if err := st.SetHomeConfig(ctx, HomeConfigScopeProfile, "profile-default", cfg, now); err != nil {
		t.Fatalf("SetHomeConfig: %v", err)
	}
	got, ok, err := st.HomeConfig(ctx, HomeConfigScopeProfile, "profile-default")
	if err != nil || !ok {
		t.Fatalf("HomeConfig: ok=%v err=%v", ok, err)
	}
	if len(got.Shelves) != 1 || got.Shelves[0].ID == "" || got.Shelves[0].Title != "Unwatched Gems" {
		t.Fatalf("unexpected saved config: %#v", got)
	}

	// Server template scope.
	if err := st.SetHomeConfig(ctx, HomeConfigScopeServerTemplate, "", cfg, now); err != nil {
		t.Fatalf("SetHomeConfig template: %v", err)
	}
	if _, ok, err := st.HomeConfig(ctx, HomeConfigScopeServerTemplate, ""); err != nil || !ok {
		t.Fatalf("template HomeConfig: ok=%v err=%v", ok, err)
	}

	// Validation: too many shelves.
	tooMany := HomeConfig{Hero: HomeHeroConfig{Enabled: true, Mode: "auto"}}
	for i := 0; i < MaxHomeShelves+1; i++ {
		tooMany.Shelves = append(tooMany.Shelves, HomeShelf{
			Title:   "S",
			Display: HomeShelfDisplay{Style: "shelf", Limit: 10},
			Source:  HomeShelfSource{Type: "builtin", BuiltinKind: "recently-added"},
		})
	}
	if err := st.SetHomeConfig(ctx, HomeConfigScopeProfile, "profile-default", tooMany, now); err == nil {
		t.Fatal("expected error for >MaxHomeShelves")
	}

	// Validation: nested rule group > max depth 2.
	deep := HomeConfig{
		Hero: HomeHeroConfig{Enabled: false, Mode: "auto"},
		Shelves: []HomeShelf{{
			Title:   "Deep",
			Display: HomeShelfDisplay{Style: "grid", Limit: 12},
			Source: HomeShelfSource{
				Type: "rules",
				Rules: &SmartRuleGroup{
					Match: "all",
					Conditions: []SmartRuleNode{{
						Group: &SmartRuleGroup{
							Match: "any",
							Conditions: []SmartRuleNode{{
								Group: &SmartRuleGroup{Match: "all"},
							}},
						},
					}},
				},
			},
		}},
	}
	if err := st.SetHomeConfig(ctx, HomeConfigScopeProfile, "profile-default", deep, now); err == nil {
		t.Fatal("expected error for depth > 2")
	}

	// Delete removes config.
	if err := st.DeleteHomeConfig(ctx, HomeConfigScopeProfile, "profile-default"); err != nil {
		t.Fatalf("DeleteHomeConfig: %v", err)
	}
	if _, ok, err := st.HomeConfig(ctx, HomeConfigScopeProfile, "profile-default"); err != nil || ok {
		t.Fatalf("expected deleted, got ok=%v err=%v", ok, err)
	}
}

func TestEvaluateSmartRulesCrossLibraryAndExtendedOperators(t *testing.T) {
	st, ids := smartFixture(t)
	ctx := context.Background()

	// Mark "Arrival" as watched, "Blue Line" as favorited & rated 8, "Cold Front" in watchlist.
	if _, err := st.SetItemWatched(ctx, "profile-default", ItemTarget{MediaID: ids["Arrival"]}, true, smartLocal); err != nil {
		t.Fatalf("SetItemWatched: %v", err)
	}
	if err := st.SetMediaFavorite(ctx, "profile-default", ids["Blue Line"], true, smartLocal); err != nil {
		t.Fatalf("SetMediaFavorite: %v", err)
	}
	if err := st.SetItemRating(ctx, "profile-default", ItemTarget{MediaID: ids["Blue Line"]}, 8, smartLocal); err != nil {
		t.Fatalf("SetItemRating: %v", err)
	}
	if err := st.SetItemWatchlisted(ctx, "profile-default", ItemTarget{MediaID: ids["Cold Front"]}, true, smartLocal); err != nil {
		t.Fatalf("SetItemWatchlisted: %v", err)
	}

	// Unwatched AND (genre IN [Rock, Jazz]) AND NOT (title is "Cold Front")
	group := SmartRuleGroup{
		Match: "all",
		Conditions: []SmartRuleNode{
			{Field: "watched", Op: "isFalse"},
			{Field: "entity:genre", Op: "in", Value: []any{"rock", "jazz"}},
			{Group: &SmartRuleGroup{
				Match:      "none",
				Conditions: []SmartRuleNode{{Field: "title", Op: "is", Value: "Cold Front"}},
			}},
		},
	}
	items, total, err := st.EvaluateSmartRules(ctx, smartLocal, nil, "profile-default", group, HomeShelfSort{By: "title"}, "", 20, 0)
	if err != nil {
		t.Fatalf("EvaluateSmartRules: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].Title != "Blue Line" {
		t.Fatalf("got items=%v total=%d, want [Blue Line]", items, total)
	}

	// Favorited OR inWatchlist
	favOrWatchlist := SmartRuleGroup{
		Match: "any",
		Conditions: []SmartRuleNode{
			{Field: "favorited", Op: "isTrue"},
			{Field: "inWatchlist", Op: "isTrue"},
		},
	}
	items2, total2, err := st.EvaluateSmartRules(ctx, smartLocal, []LibraryID{"library-local"}, "profile-default", favOrWatchlist, HomeShelfSort{By: "title"}, "", 20, 0)
	if err != nil {
		t.Fatalf("EvaluateSmartRules favOrWatchlist: %v", err)
	}
	gotTitles := []string{}
	for _, it := range items2 {
		gotTitles = append(gotTitles, it.Title)
	}
	if total2 != 2 || !slices.Equal(gotTitles, []string{"Blue Line", "Cold Front"}) {
		t.Fatalf("got %v total=%d, want [Blue Line Cold Front]", gotTitles, total2)
	}

	// Year between 1990 and 2010
	between := SmartRuleGroup{
		Match: "all",
		Conditions: []SmartRuleNode{
			{Field: "year", Op: "between", Value: []any{1990, 2010}},
		},
	}
	items3, total3, err := st.EvaluateSmartRules(ctx, smartLocal, nil, "profile-default", between, HomeShelfSort{By: "year"}, "", 20, 0)
	if err != nil {
		t.Fatalf("EvaluateSmartRules between: %v", err)
	}
	if total3 != 2 || items3[0].Title != "Blue Line" || items3[1].Title != "Cold Front" {
		t.Fatalf("between got %v total=%d", items3, total3)
	}
}
