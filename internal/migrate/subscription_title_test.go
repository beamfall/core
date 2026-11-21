package migrate

import "testing"

func TestSubscriptionImportTitleDropsURLs(t *testing.T) {
	const feedURL = "https://feeds.example.test/private/feed.xml?token=test-only-token"
	for title, want := range map[string]string{
		"":                        "",
		"  Daily News  ":          "Daily News",
		"Daily: News":             "Daily: News",
		feedURL:                   "",
		"  " + feedURL + " ":      "",
		"http://other.test/x?y=z": "",
		"FEEDS.example.test":      "",
		"other.example.test":      "other.example.test",
	} {
		if got := subscriptionImportTitle(title, feedURL); got != want {
			t.Errorf("subscriptionImportTitle(%q) = %q, want %q", title, got, want)
		}
	}
}
