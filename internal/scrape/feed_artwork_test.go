package scrape

import "testing"

func TestParseFeedArtwork(t *testing.T) {
	doc, err := ParseFeed([]byte(`<rss xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>
<title>Tower</title><description>About</description><link>https://x</link><itunes:author>Me</itunes:author>
<itunes:image href="https://x/show.jpg"/>
<item><guid>a</guid><title>A</title><itunes:image href="https://x/a.jpg"/><enclosure url="https://x/a.mp3" type="audio/mpeg"/></item>
<item><guid>b</guid><title>B</title><enclosure url="https://x/b.mp3" type="audio/mpeg"/></item>
</channel></rss>`))
	if err != nil {
		t.Fatal(err)
	}
	if doc.ArtworkURL != "https://x/show.jpg" || doc.Description != "About" || doc.Link != "https://x" || doc.Author != "Me" {
		t.Fatalf("document = %+v", doc)
	}
	if doc.Entries[0].ArtworkURL != "https://x/a.jpg" || doc.Entries[1].ArtworkURL != "" {
		t.Fatalf("entries = %+v", doc.Entries)
	}
}
