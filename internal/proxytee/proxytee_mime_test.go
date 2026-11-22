package proxytee

import "testing"

func TestProxyContentTypeArchivesApplicationMediaTypes(t *testing.T) {
	for _, raw := range []string{"application/ogg", "application/mp4", "audio/mpeg", "application/octet-stream"} {
		if header, ok := proxyContentType(raw); !ok || header != raw {
			t.Fatalf("%s = %q, %v", raw, header, ok)
		}
	}
	if header, ok := proxyContentType("text/html"); ok || header != "application/octet-stream" {
		t.Fatalf("text/html = %q, %v", header, ok)
	}
}
