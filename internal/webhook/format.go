package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Format names the request shape an endpoint receives.
type Format string

const (
	// FormatGeneric is the HMAC-signed Beamfall event envelope (the original
	// emitter format, and the default for an endpoint with no format).
	FormatGeneric Format = "generic"
	// FormatDiscord is a Discord webhook execution with one embed.
	FormatDiscord Format = "discord"
	// FormatSlack is a Slack incoming-webhook message.
	FormatSlack Format = "slack"
	// FormatNtfy publishes to an ntfy topic URL.
	FormatNtfy Format = "ntfy"
	// FormatGotify posts to a Gotify server's message endpoint.
	FormatGotify Format = "gotify"
	// FormatEmail sends a plain-text message over SMTP.
	FormatEmail Format = "email"
)

// Formats lists every endpoint format, in the order the console offers them.
var Formats = []Format{FormatGeneric, FormatDiscord, FormatSlack, FormatNtfy, FormatGotify, FormatEmail}

// ParseFormat maps an operator-supplied format to a Format. The empty string is
// the generic format, so file configs written before formats existed keep
// their behavior.
func ParseFormat(raw string) (Format, bool) {
	value := Format(strings.ToLower(strings.TrimSpace(raw)))
	if value == "" {
		return FormatGeneric, true
	}
	for _, f := range Formats {
		if f == value {
			return f, true
		}
	}
	return "", false
}

// Derived event kinds. They are computed by the emitter from the event spine,
// the Live Sessions view and the activity log; none is an outbox kind, so the
// SSE wire vocabulary is unchanged.
const (
	KindMediaAdded        = "media.added"
	KindPlaybackStarted   = "playback.started"
	KindPlaybackStopped   = "playback.stopped"
	KindProfileSwitched   = "profile.switched"
	KindAdminSignedIn     = "admin.signed_in"
	KindAdminSignInFailed = "admin.sign_in_failed"
	KindDeviceEnrolled    = "device.enrolled"
	KindDeviceRevoked     = "device.revoked"
	KindSessionTerminated = "session.terminated"
	// KindTest is the synthetic event a test-send delivers. No endpoint
	// subscribes to it; it is only ever sent to the endpoint under test.
	KindTest = "webhook.test"
)

// DerivedKinds lists every derived event kind, in console order.
var DerivedKinds = []string{
	KindMediaAdded, KindPlaybackStarted, KindPlaybackStopped, KindProfileSwitched,
	KindAdminSignedIn, KindAdminSignInFailed, KindDeviceEnrolled, KindDeviceRevoked,
	KindSessionTerminated,
}

func isDerivedKind(kind string) bool {
	for _, k := range DerivedKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Notification is one derived, human-meaningful event. Every field is display
// data the receiving owner may see; none is a secret.
type Notification struct {
	// ID is the stable delivery id sent in EventIDHeader so a receiver can
	// dedupe a retried delivery.
	ID            string
	Kind          string
	AggregateType string
	AggregateID   string
	OccurredAt    time.Time
	MediaID       string
	Title         string
	// MediaClass is the Media's class (video, audio or image): the "type" a
	// generic receiver can branch on without any domain vocabulary.
	MediaClass  string
	Year        int
	ArtworkURL  string
	ProfileName string
	DeviceName  string
	// artwork is the optional image attached by formats that can carry a file.
	artwork *Artwork
}

// Artwork is one image an endpoint format may attach to a message.
type Artwork struct {
	Data        []byte
	ContentType string
}

// notificationPayload is the generic-format payload of a derived event.
type notificationPayload struct {
	MediaID    string `json:"mediaID,omitempty"`
	Title      string `json:"title,omitempty"`
	MediaClass string `json:"mediaClass,omitempty"`
	Year       int    `json:"year,omitempty"`
	ArtworkURL string `json:"artworkURL,omitempty"`
	Profile    string `json:"profile,omitempty"`
	Device     string `json:"device,omitempty"`
	Message    string `json:"message"`
}

// genericNotificationBody serializes a derived event in the same envelope as
// a spine event (wireBody), so one receiver handles both.
func genericNotificationBody(n Notification) []byte {
	payload, err := json.Marshal(notificationPayload{
		MediaID: n.MediaID, Title: n.Title, MediaClass: n.MediaClass, Year: n.Year,
		ArtworkURL: n.ArtworkURL, Profile: n.ProfileName, Device: n.DeviceName,
		Message: n.message(),
	})
	if err != nil {
		payload = []byte("{}")
	}
	body, err := json.Marshal(struct {
		Kind          string          `json:"kind"`
		AggregateType string          `json:"aggregateType"`
		AggregateID   string          `json:"aggregateID"`
		OccurredAt    time.Time       `json:"occurredAt"`
		Payload       json.RawMessage `json:"payload"`
	}{n.Kind, n.AggregateType, n.AggregateID, n.OccurredAt, payload})
	if err != nil {
		return []byte("{}")
	}
	return body
}

// headline is the short title of a derived event.
func (n Notification) headline() string {
	switch n.Kind {
	case KindMediaAdded:
		return "New in your library"
	case KindPlaybackStarted:
		return "Playback started"
	case KindPlaybackStopped:
		return "Playback stopped"
	case KindProfileSwitched:
		return "Profile signed in"
	case KindAdminSignedIn:
		return "Admin signed in"
	case KindAdminSignInFailed:
		return "Admin sign-in failed"
	case KindDeviceEnrolled:
		return "Device enrolled"
	case KindDeviceRevoked:
		return "Device revoked"
	case KindSessionTerminated:
		return "Session ended by an admin"
	case KindTest:
		return "Beamfall test notification"
	}
	return n.Kind
}

// message is the one-line body of a derived event.
func (n Notification) message() string {
	profile := orDefault(n.ProfileName, "Someone")
	device := orDefault(n.DeviceName, "an unnamed device")
	switch n.Kind {
	case KindMediaAdded:
		return n.titleWithYear() + " was added."
	case KindPlaybackStarted:
		return fmt.Sprintf("%s started %s on %s.", profile, n.titleWithYear(), device)
	case KindPlaybackStopped:
		return fmt.Sprintf("%s stopped %s on %s.", profile, n.titleWithYear(), device)
	case KindProfileSwitched:
		return fmt.Sprintf("%s signed in on %s.", profile, device)
	case KindAdminSignedIn:
		return fmt.Sprintf("An admin signed in on %s.", device)
	case KindAdminSignInFailed:
		return fmt.Sprintf("An admin sign-in was refused on %s.", device)
	case KindDeviceEnrolled:
		return fmt.Sprintf("%s was enrolled.", orDefault(n.DeviceName, "A device"))
	case KindDeviceRevoked:
		return fmt.Sprintf("%s was revoked.", orDefault(n.DeviceName, "A device"))
	case KindSessionTerminated:
		return fmt.Sprintf("An admin ended the session on %s.", device)
	case KindTest:
		return "This endpoint is set up correctly."
	}
	return n.Kind
}

func (n Notification) titleWithYear() string {
	title := orDefault(n.Title, "Untitled")
	if n.Year > 0 {
		return fmt.Sprintf("%s (%d)", title, n.Year)
	}
	return title
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// outbound is one rendered HTTP request. fallback, when set, is sent instead
// if the target rejects the primary request with a 4xx (an ntfy server with
// attachments disabled refuses the artwork upload but accepts plain text).
type outbound struct {
	method   string
	url      string
	header   http.Header
	body     []byte
	fallback *outbound
}

const artworkFilename = "artwork"

func artworkExt(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	}
	return ".jpg"
}

// renderNotification builds the request one non-generic HTTP format sends for
// a derived event. Email is rendered by renderEmail instead.
func renderNotification(ep Endpoint, n Notification) (outbound, error) {
	switch ep.Format {
	case FormatDiscord:
		return renderDiscord(ep, n)
	case FormatSlack:
		return renderSlack(ep, n)
	case FormatNtfy:
		return renderNtfy(ep, n), nil
	case FormatGotify:
		return renderGotify(ep, n)
	}
	return outbound{}, ErrRequestDiagnostic
}

// discordColor is the embed accent (Beamfall brand amber).
const discordColor = 0xF5A524

func renderDiscord(ep Endpoint, n Notification) (outbound, error) {
	type field struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	}
	type image struct {
		URL string `json:"url"`
	}
	type embed struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Color       int     `json:"color"`
		Timestamp   string  `json:"timestamp,omitempty"`
		Fields      []field `json:"fields,omitempty"`
		Image       *image  `json:"image,omitempty"`
	}
	e := embed{
		Title:       truncateRunes(n.headline(), 256),
		Description: truncateRunes(n.message(), 4096),
		Color:       discordColor,
	}
	if !n.OccurredAt.IsZero() {
		e.Timestamp = n.OccurredAt.UTC().Format(time.RFC3339)
	}
	if n.MediaClass != "" {
		e.Fields = append(e.Fields, field{Name: "Type", Value: n.MediaClass, Inline: true})
	}
	if n.ProfileName != "" {
		e.Fields = append(e.Fields, field{Name: "Profile", Value: truncateRunes(n.ProfileName, 1024), Inline: true})
	}
	if n.DeviceName != "" {
		e.Fields = append(e.Fields, field{Name: "Device", Value: truncateRunes(n.DeviceName, 1024), Inline: true})
	}
	attachName := ""
	if n.artwork != nil && len(n.artwork.Data) > 0 {
		attachName = artworkFilename + artworkExt(n.artwork.ContentType)
		e.Image = &image{URL: "attachment://" + attachName}
	} else if n.ArtworkURL != "" {
		e.Image = &image{URL: n.ArtworkURL}
	}
	// allowed_mentions with an empty parse list stops a Media title such as
	// "@everyone" from pinging a whole server.
	payload, err := json.Marshal(struct {
		Username        string `json:"username"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
		Embeds []embed `json:"embeds"`
	}{Username: "Beamfall", Embeds: []embed{e}, AllowedMentions: struct {
		Parse []string `json:"parse"`
	}{Parse: []string{}}})
	if err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	if attachName == "" {
		return outbound{method: http.MethodPost, url: ep.URL, header: jsonHeader(), body: payload}, nil
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormField("payload_json")
	if err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	if _, err := part.Write(payload); err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	fileHeader := make(textproto.MIMEHeader)
	fileHeader.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "files[0]", "filename": attachName}))
	fileHeader.Set("Content-Type", n.artwork.ContentType)
	filePart, err := mw.CreatePart(fileHeader)
	if err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	if _, err := filePart.Write(n.artwork.Data); err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	if err := mw.Close(); err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	header := http.Header{}
	header.Set("Content-Type", mw.FormDataContentType())
	// Discord rejects an oversized upload with 413; fall back to the embed
	// without the file rather than losing the notification.
	plain := outbound{method: http.MethodPost, url: ep.URL, header: jsonHeader(), body: payload}
	return outbound{method: http.MethodPost, url: ep.URL, header: header, body: buf.Bytes(), fallback: &plain}, nil
}

// slackEscape escapes the three characters Slack's mrkdwn treats as control
// sequences, so a Media title cannot inject a mention or link.
func slackEscape(text string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(text)
}

func renderSlack(ep Endpoint, n Notification) (outbound, error) {
	text := "*" + slackEscape(n.headline()) + "*\n" + slackEscape(n.message())
	if n.MediaClass != "" {
		text += "\nType: " + slackEscape(n.MediaClass)
	}
	section := map[string]any{
		"type": "section",
		"text": map[string]any{"type": "mrkdwn", "text": truncateRunes(text, 3000)},
	}
	if n.ArtworkURL != "" {
		section["accessory"] = map[string]any{"type": "image", "image_url": n.ArtworkURL, "alt_text": truncateRunes(orDefault(n.Title, "artwork"), 2000)}
	}
	body, err := json.Marshal(map[string]any{
		// The top-level text is the notification fallback, which Slack also
		// parses for mentions, so it is escaped like the block.
		"text":   truncateRunes(slackEscape(n.headline()+": "+n.message()), 3000),
		"blocks": []any{section},
	})
	if err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	return outbound{method: http.MethodPost, url: ep.URL, header: jsonHeader(), body: body}, nil
}

// headerText makes a value safe for an HTTP header: control characters are
// dropped, and non-ASCII text is RFC 2047 encoded (ntfy decodes it).
func headerText(value string) string {
	value = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	for i := 0; i < len(value); i++ {
		if value[i] >= utf8.RuneSelf {
			return mime.BEncoding.Encode("utf-8", value)
		}
	}
	return value
}

func ntfyTags(kind string) string {
	switch kind {
	case KindMediaAdded:
		return "new"
	case KindPlaybackStarted:
		return "arrow_forward"
	case KindPlaybackStopped:
		return "stop_button"
	case KindAdminSignInFailed:
		return "warning"
	case KindDeviceRevoked, KindSessionTerminated:
		return "no_entry"
	}
	return "bell"
}

func renderNtfy(ep Endpoint, n Notification) outbound {
	header := http.Header{}
	header.Set("Title", headerText(n.headline()))
	header.Set("Tags", ntfyTags(n.Kind))
	if len(ep.Secret) > 0 {
		header.Set("Authorization", "Bearer "+string(ep.Secret))
	}
	text := outbound{method: http.MethodPost, url: ep.URL, header: header.Clone(), body: []byte(n.message())}
	text.header.Set("Content-Type", "text/plain; charset=utf-8")
	if n.artwork == nil || len(n.artwork.Data) == 0 {
		return text
	}
	// An attachment is published by PUTting the file as the body, with the
	// message moved to a header.
	header.Set("Message", headerText(n.message()))
	header.Set("Filename", artworkFilename+artworkExt(n.artwork.ContentType))
	return outbound{method: http.MethodPut, url: ep.URL, header: header, body: n.artwork.Data, fallback: &text}
}

// gotifyMessageURL returns the message endpoint for a Gotify server URL: the
// operator may paste the server root or the full /message URL.
func gotifyMessageURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrRequestDiagnostic
	}
	if !strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), "/message") {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/message"
	}
	return u.String(), nil
}

func renderGotify(ep Endpoint, n Notification) (outbound, error) {
	target, err := gotifyMessageURL(ep.URL)
	if err != nil {
		return outbound{}, err
	}
	payload := map[string]any{
		"title":    n.headline(),
		"message":  n.message(),
		"priority": gotifyPriority(n.Kind),
	}
	if n.ArtworkURL != "" {
		payload["extras"] = map[string]any{
			"client::notification": map[string]any{"bigImageUrl": n.ArtworkURL},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return outbound{}, ErrRequestDiagnostic
	}
	header := jsonHeader()
	header.Set("X-Gotify-Key", string(ep.Secret))
	return outbound{method: http.MethodPost, url: target, header: header, body: body}, nil
}

func gotifyPriority(kind string) int {
	switch kind {
	case KindAdminSignInFailed, KindDeviceRevoked:
		return 8
	case KindPlaybackStarted, KindPlaybackStopped:
		return 2
	}
	return 5
}

func jsonHeader() http.Header {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	return header
}

// truncateRunes cuts text to at most max runes, ending with an ellipsis when
// cut, so a long title never makes a target reject the whole message.
func truncateRunes(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max-1]) + "…"
}
