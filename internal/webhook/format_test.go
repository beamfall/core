package webhook

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

func sampleNotification() Notification {
	return Notification{
		ID:         "7",
		Kind:       KindMediaAdded,
		OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		MediaID:    "media-1",
		Title:      "Sample Clip",
		MediaClass: "video",
		Year:       2020,
		ArtworkURL: "https://media.example/api/media/artwork/media-1/poster",
	}
}

func TestDiscordEmbedDisablesMentions(t *testing.T) {
	n := sampleNotification()
	n.Title = "@everyone look"
	out, err := renderDiscord(Endpoint{URL: "https://discord.example/api/webhooks/1/x", Format: FormatDiscord}, n)
	if err != nil {
		t.Fatal(err)
	}
	if out.fallback != nil || out.header.Get("Content-Type") != "application/json" {
		t.Fatalf("no artwork must send plain JSON, got %q", out.header.Get("Content-Type"))
	}
	var body struct {
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
		Embeds []struct {
			Description string `json:"description"`
			Image       struct {
				URL string `json:"url"`
			} `json:"image"`
			Fields []struct{ Name, Value string } `json:"fields"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(out.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.AllowedMentions.Parse == nil || len(body.AllowedMentions.Parse) != 0 {
		t.Fatalf("allowed_mentions.parse must be an empty list, got %v", body.AllowedMentions.Parse)
	}
	if len(body.Embeds) != 1 || !strings.Contains(body.Embeds[0].Description, "@everyone look") {
		t.Fatalf("embed missing title: %+v", body.Embeds)
	}
	if body.Embeds[0].Image.URL != n.ArtworkURL {
		t.Fatalf("embed image = %q, want the artwork URL", body.Embeds[0].Image.URL)
	}
	if len(body.Embeds[0].Fields) == 0 || body.Embeds[0].Fields[0].Value != "video" {
		t.Fatalf("embed must carry the Media type: %+v", body.Embeds[0].Fields)
	}
}

func TestDiscordAttachesArtworkWithFallback(t *testing.T) {
	n := sampleNotification()
	n.artwork = &Artwork{Data: []byte("PNGDATA"), ContentType: "image/png"}
	out, err := renderDiscord(Endpoint{URL: "https://discord.example/api/webhooks/1/x", Format: FormatDiscord}, n)
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(out.header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q", out.header.Get("Content-Type"))
	}
	reader := multipart.NewReader(bytes.NewReader(out.body), params["boundary"])
	var names []string
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(part)
		names = append(names, part.FormName())
		if part.FormName() == "payload_json" && !strings.Contains(string(data), "attachment://artwork.png") {
			t.Fatalf("payload must reference the attachment: %s", data)
		}
		if part.FormName() == "files[0]" && string(data) != "PNGDATA" {
			t.Fatalf("attachment body = %q", data)
		}
	}
	if strings.Join(names, ",") != "payload_json,files[0]" {
		t.Fatalf("parts = %v", names)
	}
	if out.fallback == nil || out.fallback.header.Get("Content-Type") != "application/json" {
		t.Fatal("an attachment must carry a JSON fallback for an oversized upload")
	}
}

func TestSlackEscapesControlSequences(t *testing.T) {
	n := sampleNotification()
	n.Title = "<!channel> & <https://evil.example|x>"
	out, err := renderSlack(Endpoint{URL: "https://hooks.slack.example/services/x", Format: FormatSlack}, n)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Text   string `json:"text"`
		Blocks []struct {
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(out.body, &body); err != nil {
		t.Fatal(err)
	}
	// Both mrkdwn surfaces Slack parses for mentions must be escaped; the
	// image alt_text is plain text and is not parsed.
	for _, text := range []string{body.Text, body.Blocks[0].Text.Text} {
		if strings.Contains(text, "<!channel>") || !strings.Contains(text, "&lt;!channel&gt; &amp;") {
			t.Fatalf("slack text not escaped: %q", text)
		}
	}
}

func TestNtfyHeadersCannotBeInjected(t *testing.T) {
	n := sampleNotification()
	n.Kind = KindProfileSwitched
	n.ProfileName = "Kid\r\nX-Injected: 1"
	ep := Endpoint{URL: "https://ntfy.example/topic", Format: FormatNtfy, Secret: []byte("tok")}
	out := renderNtfy(ep, n)
	for name, values := range out.header {
		for _, v := range values {
			if strings.ContainsAny(v, "\r\n") {
				t.Fatalf("header %s carries a line break: %q", name, v)
			}
		}
	}
	if out.header.Get("Authorization") != "Bearer tok" {
		t.Fatal("ntfy token must ride the Authorization header")
	}
	if out.method != http.MethodPost || out.fallback != nil {
		t.Fatalf("no artwork: want a plain POST, got %s", out.method)
	}
}

func TestNtfyAttachmentFallsBackToText(t *testing.T) {
	n := sampleNotification()
	n.Title = "Café"
	n.artwork = &Artwork{Data: []byte("JPEG"), ContentType: "image/jpeg"}
	out := renderNtfy(Endpoint{URL: "https://ntfy.example/topic", Format: FormatNtfy}, n)
	if out.method != http.MethodPut || string(out.body) != "JPEG" {
		t.Fatalf("attachment must PUT the file, got %s %q", out.method, out.body)
	}
	if out.header.Get("Filename") != "artwork.jpg" {
		t.Fatalf("filename = %q", out.header.Get("Filename"))
	}
	if !strings.HasPrefix(out.header.Get("Message"), "=?utf-8?b?") {
		t.Fatalf("non-ASCII message header must be RFC 2047 encoded: %q", out.header.Get("Message"))
	}
	if out.fallback == nil || out.fallback.method != http.MethodPost || !strings.Contains(string(out.fallback.body), "Café") {
		t.Fatal("attachment must fall back to a text POST")
	}
	if out.header.Get("Authorization") != "" {
		t.Fatal("no token configured, no Authorization header")
	}
}

func TestGotifyTargetsMessageEndpoint(t *testing.T) {
	for _, raw := range []string{"https://gotify.example", "https://gotify.example/", "https://gotify.example/message"} {
		out, err := renderGotify(Endpoint{URL: raw, Format: FormatGotify, Secret: []byte("apptoken")}, sampleNotification())
		if err != nil {
			t.Fatal(err)
		}
		if out.url != "https://gotify.example/message" {
			t.Fatalf("%s -> %s", raw, out.url)
		}
		if out.header.Get("X-Gotify-Key") != "apptoken" {
			t.Fatal("gotify token must ride X-Gotify-Key")
		}
		if strings.Contains(out.url, "apptoken") {
			t.Fatal("gotify token must never ride the URL")
		}
	}
}

func TestEmailRejectsHeaderInjection(t *testing.T) {
	base := Endpoint{URL: "smtps://mail.example", Format: FormatEmail, From: "Beamfall <bf@example.com>", To: []string{"owner@example.com"}}
	if err := validateEmailEndpoint(base); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	bad := base
	bad.To = []string{"owner@example.com\r\nBcc: x@example.com"}
	if validateEmailEndpoint(bad) == nil {
		t.Fatal("a recipient with a line break must be rejected")
	}
	bad = base
	bad.Username = "user"
	if validateEmailEndpoint(bad) == nil {
		t.Fatal("a username without a password must be rejected")
	}

	n := sampleNotification()
	n.Title = "Clip\r\nBcc: x@example.com"
	raw := buildEmail(base, renderEmail(base, n), time.Unix(0, 0))
	headers, _, _ := strings.Cut(string(raw), "\r\n\r\n")
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), "bcc:") {
			t.Fatalf("subject injected a header: %q", line)
		}
	}
	if !strings.Contains(headers, "Auto-Submitted: auto-generated") {
		t.Fatal("email must be marked auto-generated")
	}
}

func TestValidKindsPerFormat(t *testing.T) {
	cases := []struct {
		format Format
		kinds  []string
		ok     bool
	}{
		{FormatGeneric, []string{"media.created", KindMediaAdded}, true},
		{FormatDiscord, []string{KindPlaybackStarted}, true},
		{FormatDiscord, []string{"media.created"}, false},
		{FormatGeneric, []string{"Media Created"}, false},
		{FormatGeneric, []string{strings.Repeat("a", 65)}, false},
		{FormatNtfy, nil, true},
	}
	for _, c := range cases {
		if got := validKinds(c.format, c.kinds); got != c.ok {
			t.Errorf("validKinds(%s, %v) = %v, want %v", c.format, c.kinds, got, c.ok)
		}
	}
	tooMany := make([]string, maxEventKinds+1)
	for i := range tooMany {
		tooMany[i] = KindMediaAdded
	}
	if validKinds(FormatGeneric, tooMany) {
		t.Fatal("an oversized filter must be rejected")
	}
}

func TestNonGenericEndpointIgnoresSpineKinds(t *testing.T) {
	ep := Endpoint{Format: FormatDiscord}
	if ep.matches("media.created") {
		t.Fatal("a chat format must not receive raw spine kinds")
	}
	if !ep.matches(KindMediaAdded) {
		t.Fatal("an empty filter accepts every derived kind")
	}
}
