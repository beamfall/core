package scrape

import "testing"

func FuzzResolveJSONPath(f *testing.F) {
	f.Add([]byte(`{"name":"Vega","nested":{"inner":{"deep":"value"}},"list":[{"name":"first"},{"name":"second"}]}`), "list.0.name")
	f.Add([]byte(`["a","b"]`), "1")
	f.Add([]byte(`not json`), "anything")

	f.Fuzz(func(t *testing.T, data []byte, path string) {
		root, err := decodeJSON(data)
		if err != nil {
			return
		}
		_, _ = resolveJSONPath(root, path)
		_, _ = resolveJSONNode(root, path)
	})
}

func FuzzResolveXMLPath(f *testing.F) {
	f.Add([]byte(testFeed), "item.0.enclosure.url")
	f.Add([]byte(ingestTestFeed), "item.0.guid")
	f.Add([]byte("<rss><unclosed"), "item.0.title")

	f.Fuzz(func(t *testing.T, data []byte, path string) {
		root, err := parseXML(data)
		if err != nil {
			return
		}
		_, _ = resolveXMLPath(root, path)
	})
}

func FuzzParseFeedEntries(f *testing.F) {
	f.Add([]byte(ingestTestFeed))
	f.Add([]byte(`<?xml version="1.0" encoding="utf-8"?><feed><entry><id>urn:entry</id><title>Entry</title><link rel="enclosure" href="https://media.example.test/one.mp3" type="audio/mpeg"/></entry></feed>`))
	f.Add([]byte("not xml <<<"))

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ParseFeedEntries(data)
	})
}

func FuzzScrapeTransforms(f *testing.F) {
	f.Add("year-from-date", "2014-09-05")
	f.Add("date-from-rfc822", "Wed, 11 Jun 2026 12:00:00 GMT")
	f.Add("seconds-from-clock", "01:02:03")
	f.Add("unknown", "")

	f.Fuzz(func(t *testing.T, kind, value string) {
		_, _ = transform(kind, value)
	})
}
